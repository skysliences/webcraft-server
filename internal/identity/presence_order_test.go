package identity

import (
	"encoding/json"
	"fmt"
	"io"
	api "mcraft/backend/pkg/plugin"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// The first online notification deliberately remains in a real plugin process
// while a disconnect is submitted, exercising state/notification concurrency.
func TestPresenceOrderingPluginProcess(t *testing.T) {
	if os.Args[len(os.Args)-1] != "presence-order-fixture" {
		return
	}
	p := api.New()
	for _, name := range []string{api.AccountPresenceUpdated, api.AccountPresenceOffline} {
		p.On(name, api.Subscription{Priority: api.Monitor}, func(c *api.Context, e *api.Event) error {
			if e.Name == api.AccountPresenceUpdated && e.String("connectionId") == strings.Repeat("1", 32) {
				if err := os.WriteFile(filepath.Join(c.DataDir, "entered"), []byte("online notification in progress"), 0600); err != nil {
					return err
				}
				deadline := time.Now().Add(750 * time.Millisecond)
				for {
					if _, err := os.Stat(filepath.Join(c.DataDir, "release")); err == nil {
						break
					}
					if time.Now().After(deadline) {
						return fmt.Errorf("fixture notification release timed out")
					}
					time.Sleep(time.Millisecond)
				}
			}
			raw, err := json.Marshal(e)
			if err != nil {
				return err
			}
			f, err := os.OpenFile(filepath.Join(c.DataDir, "events.jsonl"), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
			if err != nil {
				return err
			}
			_, err = f.Write(append(raw, '\n'))
			if closeErr := f.Close(); err == nil {
				err = closeErr
			}
			return err
		})
	}
	if err := p.Serve(os.Stdin, os.Stdout); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func presenceOrderingPlugin(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "presence-order")
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	from, err := os.Open(executable)
	if err != nil {
		t.Fatal(err)
	}
	defer from.Close()
	to, err := os.OpenFile(filepath.Join(path, "plugin"), os.O_CREATE|os.O_WRONLY, 0700)
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.Copy(to, from)
	to.Close()
	if err != nil {
		t.Fatal(err)
	}
	manifest := `{"id":"presence-order","name":"Presence ordering fixture","version":"1","apiVersion":1,"entry":"plugin","args":["-test.run=^TestPresenceOrderingPluginProcess$","presence-order-fixture"]}`
	if err := os.WriteFile(filepath.Join(path, "plugin.json"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func presenceOrderingFixture(t *testing.T, store Store) (*Service, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := New(Config{DataDir: dir, Issuer: "http://localhost", PluginDir: presenceOrderingPlugin(t), Clients: []Client{{ID: "testserver", Name: "并发测试服务器", FrontendURL: "http://localhost:8080", CallbackURL: "http://localhost:8080/api/auth/callback"}}}, store)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, dir
}
func presenceOrderingEvents(t *testing.T, dir string) []api.Event {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "plugins", "presence-order", "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var events []api.Event
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var e api.Event
		if json.Unmarshal([]byte(line), &e) != nil {
			t.Fatal("invalid presence event fixture")
		}
		events = append(events, e)
	}
	return events
}

type presenceValidationNoticeStore struct {
	*consoleStore
	noticeMu  sync.Mutex
	validated chan struct{}
}

func (s *presenceValidationNoticeStore) Current(hash string) *Profile {
	u := s.consoleStore.Current(hash)
	s.noticeMu.Lock()
	if s.validated != nil && hash == Hash(strings.Repeat("c", 64)) {
		close(s.validated)
		s.validated = nil
	}
	s.noticeMu.Unlock()
	return u
}

func TestPresenceConcurrentNotificationsKeepPerConnectionOrder(t *testing.T) {
	store := &presenceValidationNoticeStore{consoleStore: newConsoleStore()}
	s, dir := presenceOrderingFixture(t, store)
	token := strings.Repeat("c", 64)
	nonce := strings.Repeat("1", 32)
	onlineDone := make(chan *httptest.ResponseRecorder, 1)
	go func() { onlineDone <- websiteHeartbeat(t, s, token, nonce, true) }()
	pluginData := filepath.Join(dir, "plugins", "presence-order")
	deadline := time.Now().Add(500 * time.Millisecond)
	for {
		if _, err := os.Stat(filepath.Join(pluginData, "entered")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("online notification never entered the real SDK plugin")
		}
		time.Sleep(time.Millisecond)
	}
	// Release even when an assertion fails, so cleanup cannot strand the plugin.
	defer os.WriteFile(filepath.Join(pluginData, "release"), nil, 0600)
	validated := make(chan struct{})
	store.noticeMu.Lock()
	store.validated = validated
	store.noticeMu.Unlock()
	offlineDone := make(chan *httptest.ResponseRecorder, 1)
	go func() { offlineDone <- websiteHeartbeat(t, s, token, nonce, false) }()
	select {
	case <-validated:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("disconnect request did not pass initial validation")
	}
	select {
	case w := <-offlineDone:
		t.Fatalf("disconnect finished before in-progress online notification: %d", w.Code)
	case <-time.After(25 * time.Millisecond):
	}
	s.presence.mu.Lock()
	_, remainsOnline := s.presence.entries[nonce]
	s.presence.mu.Unlock()
	if !remainsOnline {
		t.Fatal("presence changed to offline while its online notification was still running")
	}
	if err := os.WriteFile(filepath.Join(pluginData, "release"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	for _, done := range []chan *httptest.ResponseRecorder{onlineDone, offlineDone} {
		select {
		case w := <-done:
			if w.Code != 200 {
				t.Fatalf("concurrent report %d %s", w.Code, w.Body.String())
			}
		case <-time.After(time.Second):
			t.Fatal("presence operation did not finish after notification release")
		}
	}
	// Different nonce lifecycles run concurrently; every nonce still receives
	// exactly one ordered online/offline pair in the external plugin's journal.
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			connection := fmt.Sprintf("%032x", n+100)
			for _, online := range []bool{true, false} {
				if w := websiteHeartbeat(t, s, token, connection, online); w.Code != 200 {
					t.Errorf("nonce lifecycle report %d %s", w.Code, w.Body.String())
				}
			}
		}(i)
	}
	wg.Wait()
	byNonce := map[string][]api.Event{}
	for _, e := range presenceOrderingEvents(t, dir) {
		byNonce[e.String("connectionId")] = append(byNonce[e.String("connectionId")], e)
	}
	if len(byNonce) != 25 {
		t.Fatalf("missing nonce lifecycle events: got %d", len(byNonce))
	}
	for connection, events := range byNonce {
		if len(events) != 2 || events[0].Name != api.AccountPresenceUpdated || events[1].Name != api.AccountPresenceOffline || events[0].ID >= events[1].ID || events[0].String("reason") != "website_heartbeat" || events[1].String("reason") != "website_leave" {
			t.Fatalf("unordered lifecycle for %s: %v", connection, events)
		}
		for _, e := range events {
			if e.Identity == nil || e.Identity.ID != testUserID || e.String("actor") != testUserID || e.String("target") != testUserID || e.String("serverId") != "" || e.String("location") != "website" {
				t.Fatal("presence lifecycle omitted the stable identity or website location")
			}
		}
	}
	if len(s.adminPresence()) != 0 {
		t.Fatal("completed disconnects left phantom online connections")
	}
}

// Return a valid snapshot once, but revoke its session before the commit pass.
type presenceRevokingValidationStore struct {
	*consoleStore
	validationMu sync.Mutex
	armed        bool
	calls        int
	target       string
}

func (s *presenceRevokingValidationStore) Current(hash string) *Profile {
	u := s.consoleStore.Current(hash)
	s.validationMu.Lock()
	if s.armed && hash == s.target {
		s.calls++
		if s.calls == 1 {
			s.consoleStore.Logout(hash)
		}
	}
	s.validationMu.Unlock()
	return u
}

func TestWebsitePresenceCommitRevalidatesRevokedSession(t *testing.T) {
	token := strings.Repeat("d", 64)
	store := &presenceRevokingValidationStore{consoleStore: newConsoleStore(), target: Hash(token)}
	s, dir := presenceOrderingFixture(t, store)
	baseline := strings.Repeat("2", 32)
	if w := websiteHeartbeat(t, s, strings.Repeat("a", 64), baseline, true); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	store.validationMu.Lock()
	store.armed = true
	store.validationMu.Unlock()
	w := websiteHeartbeat(t, s, token, strings.Repeat("4", 32), true)
	if w.Code != 401 {
		t.Fatalf("revoked during validation should reject commit: %d %s", w.Code, w.Body.String())
	}
	store.validationMu.Lock()
	calls := store.calls
	store.validationMu.Unlock()
	if calls != 2 {
		t.Fatalf("expected initial and commit validation, got %d calls", calls)
	}
	s.presence.mu.Lock()
	count := len(s.presence.entries)
	_, existing := s.presence.entries[baseline]
	s.presence.mu.Unlock()
	if count != 1 || !existing {
		t.Fatal("revoked heartbeat committed or changed an existing tab")
	}
	events := presenceOrderingEvents(t, dir)
	if len(events) != 1 || events[0].Name != api.AccountPresenceUpdated || events[0].String("connectionId") != baseline {
		t.Fatal("rejected heartbeat emitted a successful presence event")
	}
}
