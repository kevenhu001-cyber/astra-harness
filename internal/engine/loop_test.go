package engine

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/kevenhu001-cyber/astra-harness/internal/core"
)

func TestEngineLoopWithMockProvider(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = body
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)
		n := requests.Add(1)
		switch n {
		case 1:
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"function\":{\"name\":\"search\",\"arguments\":\"\"}}]}}]}\n\n")
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"{\\\"query\\\":\\\"runServer\\\"}\"}}]}}]}\n\n")
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
		default:
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Server found. \"}}]}\n\n")
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Done.\"}}]}\n\n")
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
		}
		if fl != nil {
			fl.Flush()
		}
	}))
	defer srv.Close()

	t.Setenv("MOCK_KEY", "test-key")
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module demo\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc runServer() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	auto := false
	cfg := &Config{
		Providers: []ProviderConfig{{
			ID: "mock", Type: "openai-compatible", Name: "Mock",
			BaseURL: srv.URL, APIKeyEnv: "MOCK_KEY", Models: []string{"mock-model"},
		}},
		DefaultProvider: "mock", PermissionMode: ModeAllow,
		MaxIterations: 5, MaxContextTokens: 100000, AutoVerify: &auto,
		TimeoutSeconds: 10,
	}
	eng, err := NewEngine(root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Store.Close()
	if eng.ProviderID() != "mock" {
		t.Fatalf("provider = %s", eng.ProviderID())
	}
	if err := eng.Run(context.Background(), "find runServer"); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 2 {
		t.Fatalf("expected 2 model requests, got %d", requests.Load())
	}
	var searchFound bool
	for _, a := range eng.Store.State.Actions {
		if a.Tool == "search" && a.Status == core.StatusSucceeded {
			searchFound = true
			if a.Type != core.ActionSearch {
				t.Fatalf("action type = %s", a.Type)
			}
		}
	}
	if !searchFound {
		t.Fatal("expected a succeeded search action")
	}
	if len(eng.Store.State.Goals) != 1 || eng.Store.State.Goals[0].Description == "" {
		t.Fatalf("goal not created: %+v", eng.Store.State.Goals)
	}
}

func TestEngineRunMissingProviderKey(t *testing.T) {
	t.Setenv("NO_SUCH_KEY_PROVIDER_XYZ", "")
	root := t.TempDir()
	auto := true
	cfg := &Config{
		Providers: []ProviderConfig{{
			ID: "x", Type: "openai-compatible", Name: "X",
			BaseURL: "http://127.0.0.1:1/v1", APIKeyEnv: "NO_SUCH_KEY_PROVIDER_XYZ",
			Models: []string{"m"},
		}},
		PermissionMode: ModeAsk, MaxIterations: 3, AutoVerify: &auto, TimeoutSeconds: 5,
	}
	eng, err := NewEngine(root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Store.Close()
	err = eng.Run(context.Background(), "do something")
	if err == nil {
		t.Fatal("expected provider-not-configured error")
	}
}
