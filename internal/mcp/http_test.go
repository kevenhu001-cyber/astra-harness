package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeHTTPServer implements the MCP streamable-HTTP transport: JSON-RPC over
// POST, session id header on initialize, JSON responses.
func fakeHTTPServer(t *testing.T, sse bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			ID     *int            `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if req.ID == nil {
			w.WriteHeader(http.StatusAccepted) // notification
			return
		}
		var result map[string]any
		switch req.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "sess-abc")
			result = map[string]any{
				"protocolVersion": ProtocolVersion,
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "http-fake", "version": "1.0"},
			}
		case "tools/list":
			result = map[string]any{
				"tools": []map[string]any{
					{"name": "ping", "description": "pings", "inputSchema": map[string]any{"type": "object"}},
					{"name": "echo", "description": "echoes", "inputSchema": map[string]any{"type": "object"}},
				},
			}
		case "tools/call":
			var params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}
			_ = json.Unmarshal(req.Params, &params)
			text := "pong"
			if params.Name == "echo" {
				text = "echo " + params.Arguments["msg"].(string)
			}
			result = map[string]any{
				"content": []map[string]any{{"type": "text", "text": text}},
				"isError": false,
			}
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		msg := map[string]any{"jsonrpc": "2.0", "id": *req.ID, "result": result}
		data, _ := json.Marshal(msg)
		if sse {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Write([]byte("event: message\ndata: " + string(data) + "\n\n"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)
	}))
}

func TestHTTPClientEndToEnd(t *testing.T) {
	srv := fakeHTTPServer(t, false)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := StartHTTP(ctx, ServerConfig{ID: "remote", Type: "http", URL: srv.URL})
	if err != nil {
		t.Fatalf("StartHTTP: %v", err)
	}
	defer c.Close()

	tools, err := c.ListTools()
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 2 || tools[0].Name != "ping" {
		t.Fatalf("tools = %+v", tools)
	}

	res, err := c.CallTool(ctx, "ping", nil)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if strings.TrimSpace(res.Text) != "pong" || res.IsError {
		t.Fatalf("ping result = %+v", res)
	}

	res, err = c.CallTool(ctx, "echo", map[string]any{"msg": "hi"})
	if err != nil {
		t.Fatalf("CallTool echo: %v", err)
	}
	if !strings.Contains(res.Text, "echo hi") {
		t.Fatalf("echo result = %+v", res)
	}

	// Session id must be captured and resent on subsequent calls.
	if c.session != "sess-abc" {
		t.Fatalf("session = %q, want sess-abc", c.session)
	}
}

func TestHTTPClientSSEResponse(t *testing.T) {
	srv := fakeHTTPServer(t, true)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := StartHTTP(ctx, ServerConfig{ID: "remote", Type: "http", URL: srv.URL})
	if err != nil {
		t.Fatalf("StartHTTP: %v", err)
	}
	defer c.Close()

	tools, err := c.ListTools()
	if err != nil {
		t.Fatalf("ListTools over SSE: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("tools = %+v", tools)
	}
}

func TestHTTPClientError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte("no auth"))
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := StartHTTP(ctx, ServerConfig{ID: "bad", Type: "http", URL: srv.URL}); err == nil {
		t.Fatal("expected auth error")
	}
}
