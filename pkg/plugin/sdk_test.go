package plugin

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestDispatchCancellationUnsubscribeAndTick(t *testing.T) {
	p := New()
	calls := 0
	p.On(PlayerChatting, Subscription{}, func(c *Context, e *Event) error { e.Cancel("blocked"); return nil })
	p.On(PlayerChatting, Subscription{IgnoreCancelled: true}, func(c *Context, e *Event) error { t.Fatal("cancelled listener ran"); return nil })
	off := p.On(PlayerChatting, Subscription{}, func(c *Context, e *Event) error { calls++; e.Cancelled = false; return nil })
	off()
	p.Every(5, func(c *Context, e *Event) error { calls++; return nil })
	response := p.handle(Request{ID: 1, Method: "event", Event: &Event{Name: PlayerChatting, Cancellable: true}})
	if !response.Cancelled || calls != 0 {
		t.Fatalf("cancellation/unsubscribe: %+v %d", response, calls)
	}
	p.handle(Request{Method: "event", Event: &Event{Name: ServerTick, Tick: 4}})
	p.handle(Request{Method: "event", Event: &Event{Name: ServerTick, Tick: 5}})
	if calls != 1 {
		t.Fatal(calls)
	}
}
func TestServeHandshakeCommandPanicAndShutdown(t *testing.T) {
	p := New()
	enabled, disabled := false, false
	p.OnEnable = func(c *Context) error { enabled = c.DataDir == "data"; return nil }
	p.OnDisable = func(c *Context) error { disabled = true; return nil }
	p.Command(Command{Name: "hello"}, func(c *Context, e *Event, args []string) error { c.Message(e, args[0]); return nil })
	p.Command(Command{Name: "panic"}, func(c *Context, e *Event, args []string) error { panic("expected") })
	var in, out bytes.Buffer
	encoder := json.NewEncoder(&in)
	requests := []Request{{ID: 1, Method: "initialize", Initialize: &Initialize{APIVersion: APIVersion, DataDir: "data"}}, {ID: 2, Method: "command", Command: "hello", Args: []string{"hi"}, Event: &Event{World: &World{ID: "world"}, Player: &Player{ID: 7}}}, {ID: 3, Method: "command", Command: "panic", Event: &Event{}}, {ID: 4, Method: "shutdown"}}
	for _, r := range requests {
		encoder.Encode(r)
	}
	if err := p.Serve(&in, &out); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(&out)
	var responses []Response
	for range requests {
		var r Response
		if err := decoder.Decode(&r); err != nil {
			t.Fatal(err)
		}
		responses = append(responses, r)
	}
	if !enabled || !disabled || responses[0].Metadata.APIVersion != 1 || responses[1].Actions[0].Message != "hi" || responses[2].Error == "" {
		t.Fatalf("bad responses %+v", responses)
	}
}

func TestBatchBlockActionAndQueryBlocks(t *testing.T) {
	ctx := &Context{}
	changes := []BlockChange{
		{X: 10, Y: 64, Z: 10, Block: 1},
		{X: 11, Y: 64, Z: 10, Block: 2},
		{X: 12, Y: 64, Z: 10, Block: 3},
	}
	ctx.SetBlocks("world_test", changes)
	if len(ctx.Actions) != 1 {
		t.Fatalf("expected 1 action, got %d", len(ctx.Actions))
	}
	act := ctx.Actions[0]
	if act.Type != "set_blocks" || act.WorldID != "world_test" || len(act.Blocks) != 3 {
		t.Fatalf("unexpected action: %+v", act)
	}
}
