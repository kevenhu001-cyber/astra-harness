package authsrv

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func newTestServer(t *testing.T) (*httptest.Server, *Store, *Server) {
	t.Helper()
	store, err := OpenStore(filepath.Join(t.TempDir(), "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	srv := New(store, ConsoleMailer{}, Options{BaseURL: "http://test.local"})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() { ts.Close(); store.Close() })
	return ts, store, srv
}

func post(t *testing.T, url string, body any) (*http.Response, map[string]any) {
	t.Helper()
	b, _ := json.Marshal(body)
	resp, err := http.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return resp, out
}

func TestRegisterVerifyLoginFlow(t *testing.T) {
	ts, store, _ := newTestServer(t)

	// Register (Socrates mechanism: pending, no user yet).
	resp, out := post(t, ts.URL+"/api/auth/register", map[string]any{"email": "User@Example.com ", "password": "password123"})
	if resp.StatusCode != 200 || out["ok"] != true {
		t.Fatalf("register = %d %v", resp.StatusCode, out)
	}
	if store.FindUserByEmail("user@example.com") != nil {
		t.Fatal("user created before verification")
	}
	pending := store.FindPending("user@example.com")
	if pending == nil || len(pending.Token) != 32 {
		t.Fatalf("pending = %+v", pending)
	}

	// Verify via the link → user created + session cookie set. Use a client
	// that does not follow the redirect so the 302 itself is observable.
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	vr, err := client.Get(ts.URL + "/api/auth/verify?token=" + pending.Token)
	if err != nil {
		t.Fatal(err)
	}
	vr.Body.Close()
	if vr.StatusCode != http.StatusFound {
		t.Fatalf("verify status = %d", vr.StatusCode)
	}
	if store.FindUserByEmail("user@example.com") == nil {
		t.Fatal("user not created after verify")
	}
	if store.FindPending("user@example.com") != nil {
		t.Fatal("pending not cleared after verify")
	}

	// /me with the session cookie.
	me, err := client.Get(ts.URL + "/api/auth/me")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(me.Body)
	me.Body.Close()
	if me.StatusCode != 200 || !strings.Contains(string(body), "user@example.com") {
		t.Fatalf("me = %d %s", me.StatusCode, body)
	}
}

func TestLoginWrongPassword(t *testing.T) {
	ts, store, _ := newTestServer(t)
	store.CreateUser(&User{ID: "u1", Email: "a@b.co", PasswordHash: mustHash(t, "correct-horse")})

	resp, out := post(t, ts.URL+"/api/auth/login", map[string]any{"email": "a@b.co", "password": "wrong"})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("login status = %d (%v)", resp.StatusCode, out)
	}
	if !strings.Contains(out["error"].(string), "invalid email or password") {
		t.Fatalf("error = %v", out["error"])
	}
}

func TestDuplicateRegistrationIsSilent(t *testing.T) {
	ts, store, _ := newTestServer(t)
	store.CreateUser(&User{ID: "u1", Email: "a@b.co", PasswordHash: mustHash(t, "password123")})

	resp, out := post(t, ts.URL+"/api/auth/register", map[string]any{"email": "a@b.co", "password": "password123"})
	if resp.StatusCode != 200 || out["ok"] != true {
		t.Fatalf("duplicate register = %d %v (must stay silent, anti-enumeration)", resp.StatusCode, out)
	}
}

func TestValidationRules(t *testing.T) {
	cases := []struct {
		email, pass string
		bad         bool
	}{
		{"a@b.co", "short", true},                 // < 8 chars
		{"a@b.co", "longpassword", false},         // ok
		{"a@b.co", strings.Repeat("x", 65), true}, // > 64 chars
		{"not-an-email", "password123", true},     // bad email
	}
	for _, c := range cases {
		err := ValidateCredentials(c.email, c.pass)
		if c.bad && err == nil {
			t.Errorf("expected error for %q/%d", c.email, len(c.pass))
		}
		if !c.bad && err != nil {
			t.Errorf("unexpected error for %q: %v", c.email, err)
		}
	}
}

func TestDeviceFlowEndToEnd(t *testing.T) {
	ts, store, _ := newTestServer(t)

	// Seed a user + browser session (as if the user registered and logged in).
	store.CreateUser(&User{ID: "u1", Email: "dev@b.co", PasswordHash: mustHash(t, "password123")})
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	if _, err := client.Post(ts.URL+"/api/auth/login", "application/json",
		strings.NewReader(`{"email":"dev@b.co","password":"password123"}`)); err != nil {
		t.Fatal(err)
	}

	// 1) CLI requests a device grant.
	_, out := post(t, ts.URL+"/api/auth/device", nil)
	deviceCode, _ := out["device_code"].(string)
	userCode, _ := out["user_code"].(string)
	uri, _ := out["verification_uri"].(string)
	if deviceCode == "" || userCode == "" || !strings.Contains(uri, userCode) {
		t.Fatalf("device = %v", out)
	}

	// 2) TUI polls → still pending.
	_, poll := post(t, ts.URL+"/api/auth/device/token", map[string]any{"device_code": deviceCode})
	if poll["status"] != "pending" {
		t.Fatalf("poll = %v", poll)
	}

	// 3) User approves in the browser (authenticated session).
	body := strings.NewReader(`{"user_code":"` + userCode + `"}`)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/auth/device/approve", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", ts.URL)
	ar, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(ar.Body)
	ar.Body.Close()
	if ar.StatusCode != http.StatusOK {
		t.Fatalf("approve status = %d (%s)", ar.StatusCode, raw)
	}

	// 4) TUI polls → approved with a bearer token.
	_, poll2 := post(t, ts.URL+"/api/auth/device/token", map[string]any{"device_code": deviceCode})
	if poll2["status"] != "approved" {
		t.Fatalf("poll2 = %v", poll2)
	}
	token, _ := poll2["access_token"].(string)
	if token == "" {
		t.Fatal("no access token")
	}

	// 5) Token authenticates /me.
	req, _ = http.NewRequest(http.MethodGet, ts.URL+"/api/auth/me", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	mr, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw2, _ := io.ReadAll(mr.Body)
	mr.Body.Close()
	if mr.StatusCode != 200 || !strings.Contains(string(raw2), "dev@b.co") {
		t.Fatalf("bearer me = %d %s", mr.StatusCode, raw2)
	}
}

func TestCrossSiteOriginRejected(t *testing.T) {
	ts, store, _ := newTestServer(t)
	store.CreateUser(&User{ID: "u1", Email: "a@b.co", PasswordHash: mustHash(t, "password123")})

	body := strings.NewReader(`{"email":"a@b.co","password":"password123"}`)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/auth/login", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://evil.example")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-site status = %d, want 403", resp.StatusCode)
	}
}

func TestSitePagesServe(t *testing.T) {
	ts, _, _ := newTestServer(t)
	for _, p := range []string{"/", "/login.html", "/authorize.html", "/account.html", "/assets/site.css", "/assets/auth.js", "/favicon.svg"} {
		resp, err := http.Get(ts.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Errorf("%s = %d", p, resp.StatusCode)
		}
	}
}

func mustHash(t *testing.T, pw string) string {
	t.Helper()
	h, err := hashPassword(pw)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestDisplayNameInMe(t *testing.T) {
	ts, store, _ := newTestServer(t)
	store.CreateUser(&User{ID: "u1", Email: "u@b.co", PasswordHash: mustHash(t, "password123"), DisplayName: "Astra Tester"})

	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	if _, err := c.Post(ts.URL+"/api/auth/login", "application/json",
		strings.NewReader(`{"email":"u@b.co","password":"password123"}`)); err != nil {
		t.Fatal(err)
	}
	r, err := c.Get(ts.URL + "/api/auth/me")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if r.StatusCode != 200 {
		t.Fatalf("me = %d %s", r.StatusCode, raw)
	}
	var out struct {
		User struct {
			Email       string `json:"email"`
			DisplayName string `json:"display_name"`
		} `json:"user"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out.User.DisplayName != "Astra Tester" {
		t.Fatalf("display_name = %q, want %q", out.User.DisplayName, "Astra Tester")
	}
}

func TestAccountUpdateDisplayName(t *testing.T) {
	ts, store, _ := newTestServer(t)
	store.CreateUser(&User{ID: "u1", Email: "u@b.co", PasswordHash: mustHash(t, "password123")})

	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	if _, err := c.Post(ts.URL+"/api/auth/login", "application/json",
		strings.NewReader(`{"email":"u@b.co","password":"password123"}`)); err != nil {
		t.Fatal(err)
	}

	// unauthenticated update should be 401
	resp, _ := http.Post(ts.URL+"/api/auth/account", "application/json",
		strings.NewReader(`{"display_name":"x"}`))
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauth account update = %d, want 401", resp.StatusCode)
	}

	// authenticated update
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/auth/account",
		strings.NewReader(`{"display_name":"  New Name  "}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", ts.URL)
	ar, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(ar.Body)
	ar.Body.Close()
	if ar.StatusCode != 200 {
		t.Fatalf("account update = %d %s", ar.StatusCode, raw)
	}

	// /me reflects the trimmed name
	mr, err := c.Get(ts.URL + "/api/auth/me")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = io.ReadAll(mr.Body)
	mr.Body.Close()
	if !strings.Contains(string(raw), `"display_name":"New Name"`) {
		t.Fatalf("me body = %s", raw)
	}

	// and the store row was updated
	if got := store.FindUserByID("u1"); got == nil || got.DisplayName != "New Name" {
		t.Fatalf("store.DisplayName = %+v", got)
	}

	// over-long input is truncated to 60 chars
	long := strings.Repeat("a", 100)
	req, _ = http.NewRequest(http.MethodPost, ts.URL+"/api/auth/account",
		strings.NewReader(`{"display_name":"`+long+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", ts.URL)
	lr, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	lr.Body.Close()
	if got := store.FindUserByID("u1"); got == nil || len(got.DisplayName) != 60 {
		t.Fatalf("truncation failed, len=%d", len(got.DisplayName))
	}
}
