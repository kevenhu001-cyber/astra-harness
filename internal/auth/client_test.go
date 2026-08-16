package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestLoginDeviceFlow(t *testing.T) {
	var polls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/auth/device":
			json.NewEncoder(w).Encode(map[string]any{
				"device_code": "dc1", "user_code": "ABCD-EFGH",
				"verification_uri": "http://site/authorize.html?code=ABCD-EFGH",
				"expires_in":       600, "interval": 1,
			})
		case "/api/auth/device/token":
			n := polls.Add(1)
			if n < 2 {
				json.NewEncoder(w).Encode(map[string]any{"status": "pending"})
				return
			}
			json.NewEncoder(w).Encode(map[string]any{
				"status": "approved", "access_token": "tok-1",
				"user": map[string]any{"id": "u1", "email": "cli@b.co"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	cred, err := Login(context.Background(), srv.URL, &discard{})
	if err != nil {
		t.Fatal(err)
	}
	if cred.Token != "tok-1" || cred.User.Email != "cli@b.co" {
		t.Fatalf("cred = %+v", cred)
	}
	if polls.Load() < 2 {
		t.Fatalf("expected at least 2 polls, got %d", polls.Load())
	}
}

func TestLoginExpired(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/auth/device":
			json.NewEncoder(w).Encode(map[string]any{
				"device_code": "dc1", "user_code": "ABCD-EFGH",
				"verification_uri": "http://site/authorize.html",
				"expires_in":       1, "interval": 1,
			})
		case "/api/auth/device/token":
			json.NewEncoder(w).Encode(map[string]any{"status": "expired"})
		}
	}))
	defer srv.Close()

	_, err := Login(context.Background(), srv.URL, &discard{})
	if err == nil {
		t.Fatal("expected expiry error")
	}
}

func TestCredentialRoundtrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ASTRA_CONFIG_DIR", filepath.Join(dir, "cfg"))
	path := CredentialPath()
	if path == "" {
		t.Fatal("empty credential path")
	}
	if _, err := os.Stat(filepath.Dir(path)); os.IsNotExist(err) {
		// LoadCredential with no file → nil, nil
	}
	cred, err := LoadCredential()
	if err != nil || cred != nil {
		t.Fatalf("load empty = %v, %v", cred, err)
	}
	c := &Credential{Server: "http://x", Token: "tok", User: User{ID: "u1", Email: "a@b.co"}, ExpiresAt: time.Now().Add(time.Hour)}
	if err := SaveCredential(c); err != nil {
		t.Fatal(err)
	}
	got, err := LoadCredential()
	if err != nil {
		t.Fatal(err)
	}
	if got.Token != "tok" || got.User.Email != "a@b.co" {
		t.Fatalf("got = %+v", got)
	}
	if err := ClearCredential(); err != nil {
		t.Fatal(err)
	}
	got2, _ := LoadCredential()
	if got2 != nil {
		t.Fatal("credential not cleared")
	}
}

func TestMeWithBearer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer good" {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"user": map[string]any{"id": "u1", "email": "me@b.co"}})
	}))
	defer srv.Close()

	c := New(srv.URL)
	u, err := c.Me(context.Background(), "good")
	if err != nil || u.Email != "me@b.co" {
		t.Fatalf("me = %+v, %v", u, err)
	}
	if _, err := c.Me(context.Background(), "bad"); err == nil {
		t.Fatal("expected error for bad token")
	}
}

func TestMeReturnsDisplayName(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"user": map[string]any{
				"id":           "u1",
				"email":        "name@b.co",
				"display_name": "Astra Tester",
			},
		})
	}))
	defer srv.Close()
	c := New(srv.URL)
	u, err := c.Me(context.Background(), "any")
	if err != nil {
		t.Fatal(err)
	}
	if u.DisplayName != "Astra Tester" {
		t.Fatalf("display_name = %q, want %q", u.DisplayName, "Astra Tester")
	}
}

type discard struct{}

func (d *discard) Write(p []byte) (int, error) { return len(p), nil }
