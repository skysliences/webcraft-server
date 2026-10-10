package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"time"
)

type Profile struct {
	ID        string `json:"id"`
	Username  string `json:"username"`
	Name      string `json:"name"`
	LegacySID string `json:"legacySid,omitempty"`
}
type Store interface {
	Current(string) *Profile
	Register(string, string, string) (*Profile, string, error)
	Login(string, string, string) (*Profile, string, error)
	SetName(string, string) (*Profile, error)
	Logout(string) error
	Allow(*http.Request) bool
	Close() error
}
type Client struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	OwnerID     string `json:"ownerId,omitempty"`
	FrontendURL string `json:"frontendUrl"`
	CallbackURL string `json:"callbackUrl"`
}
type Claims struct {
	Issuer   string  `json:"iss"`
	Audience string  `json:"aud"`
	Subject  string  `json:"sub"`
	IssuedAt int64   `json:"iat"`
	Expires  int64   `json:"exp"`
	Session  string  `json:"session"`
	Profile  Profile `json:"profile"`
	Purpose  string  `json:"purpose,omitempty"`
	GrantID  string  `json:"grantId,omitempty"`
}

var IDPattern = regexp.MustCompile(`^[A-Za-z0-9]{12,40}$`)
var ServerPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
var pkcePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
var verifierPattern = regexp.MustCompile(`^[A-Za-z0-9._~-]{43,128}$`)

func Random() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func Hash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
func Challenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
func WebURL(value string) (*url.URL, error) {
	u, err := url.Parse(value)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, errors.New("需要不包含凭据、参数的 HTTP/HTTPS 地址")
	}
	if u.Scheme == "http" && u.Hostname() != "localhost" {
		ip := net.ParseIP(u.Hostname())
		if ip == nil || !ip.IsLoopback() {
			return nil, errors.New("线上账号连接必须使用 HTTPS；本机调试允许 HTTP")
		}
	}
	return u, nil
}
func Sign(key ed25519.PrivateKey, claims Claims) (string, error) {
	raw, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	payload := base64.RawURLEncoding.EncodeToString(raw)
	return payload + "." + base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, []byte(payload))), nil
}
func Verify(key ed25519.PublicKey, token, issuer, audience string, now time.Time) (*Claims, error) {
	if len(token) > 8192 {
		return nil, errors.New("invalid identity proof")
	}
	var payload, signature string
	for i, b := range token {
		if b == '.' {
			payload, signature = token[:i], token[i+1:]
			break
		}
	}
	sig, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil || len(key) != ed25519.PublicKeySize || !ed25519.Verify(key, []byte(payload), sig) {
		return nil, errors.New("invalid identity signature")
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return nil, err
	}
	var c Claims
	if err = json.Unmarshal(raw, &c); err != nil {
		return nil, err
	}
	if c.Issuer != issuer || c.Audience != audience || !IDPattern.MatchString(c.Subject) || c.Profile.ID != c.Subject || c.Profile.Name == "" || len(c.Session) != 64 || c.Expires <= now.Unix() || c.Expires <= c.IssuedAt || c.IssuedAt > now.Add(30*time.Second).Unix() || c.Expires-c.IssuedAt > 600 || c.IssuedAt <= 0 {
		return nil, errors.New("invalid identity claims")
	}
	return &c, nil
}
