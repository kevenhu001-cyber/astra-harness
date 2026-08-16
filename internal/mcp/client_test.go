package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"
)

// fakeServer implements just enough of the MCP server side to exercise the
// client: initialize handshake, tools/list and tools/call.
type fakeServer struct {
	reader *bufio.Scanner
	writer io.WriteCloser
	tools  []Tool
}

func (s *fakeServer) run(t *testing.T) {
	for s.reader.Scan() {
		line := strings.TrimSpace(s.reader.Text())
		if line == "" {
			continue
		}
		var req struct {
			ID     *int            `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			t.Fatalf("server: bad request %q: %v", line, err)
		}
		if req.ID == nil {
			continue // notification (e.g. notifications/initialized)
		}
		switch req.Method {
		case "initialize":
			s.reply(t, *req.ID, map[string]any{
				"protocolVersion": ProtocolVersion,
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "fake", "version": "1.0"},
			})
		case "tools/list":
			s.reply(t, *req.ID, map[string]any{"tools": s.tools})
		case "tools/call":
			var params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}
			_ = json.Unmarshal(req.Params, &params)
			if params.Name == "boom" {
				s.reply(t, *req.ID, map[string]any{
					"content": []map[string]any{{"type": "text", "text": "exploded"}},
					"isError": true,
				})
				continue
			}
			s.reply(t, *req.ID, map[string]any{
				"content": []map[string]any{{"type": "text", "text": "echo " + params.Name}},
			})
		default:
			s.replyError(t, *req.ID, -32601, "method not found")
		}
	}
}

func (s *fakeServer) reply(t *testing.T, id int, result any) {
	t.Helper()
	msg := map[string]any{"jsonrpc": "2.0", "id": id, "result": result}
	data, _ := json.Marshal(msg)
	if _, err := s.writer.Write(append(data, '\n')); err != nil {
		t.Fatalf("server reply: %v", err)
	}
}

func (s *fakeServer) replyError(t *testing.T, id int, code int, message string) {
	t.Helper()
	msg := map[string]any{
		"jsonrpc": "2.0", "id": id,
		"error": map[string]any{"code": code, "message": message},
	}
	data, _ := json.Marshal(msg)
	if _, err := s.writer.Write(append(data, '\n')); err != nil {
		t.Fatalf("server reply: %v", err)
	}
}

// newPipedClient wires a client to a fake server over in-memory pipes.
func newPipedClient(t *testing.T, tools []Tool) (*Client, *fakeServer) {
	t.Helper()
	// Pipe A: client→server. Pipe B: server→client. io.Pipe returns (reader, writer).
	serverR, clientW := io.Pipe()
	clientR, serverW := io.Pipe()
	srv := &fakeServer{reader: bufio.NewScanner(serverR), writer: serverW, tools: tools}
	done := make(chan struct{})
	go func() {
		defer close(done)
		srv.run(t)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := startWithIO(ctx, ServerConfig{ID: "fake"}, clientW, clientR)
	if err != nil {
		t.Fatalf("startWithIO: %v", err)
	}
	t.Cleanup(func() {
		_ = client.Close()
		clientW.Close()
		serverW.Close()
		<-done
	})
	return client, srv
}

func TestInitializeAndListTools(t *testing.T) {
	client, _ := newPipedClient(t, []Tool{
		{Name: "search", Description: "search docs", InputSchema: map[string]any{"type": "object"}},
		{Name: "read", Description: "read a doc"},
	})
	tools, err := client.ListTools()
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 2 || tools[0].Name != "search" || tools[1].Name != "read" {
		t.Fatalf("tools = %+v", tools)
	}
	if names := client.ToolNames(); len(names) != 2 || names[0] != "search" {
		t.Fatalf("ToolNames = %v", names)
	}
}

func TestCallTool(t *testing.T) {
	client, _ := newPipedClient(t, []Tool{{Name: "greet"}})
	res, err := client.CallTool(context.Background(), "greet", map[string]any{"who": "world"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if strings.TrimSpace(res.Text) != "echo greet" || res.IsError {
		t.Fatalf("result = %+v", res)
	}
}

func TestCallToolError(t *testing.T) {
	client, _ := newPipedClient(t, []Tool{{Name: "boom"}})
	res, err := client.CallTool(context.Background(), "boom", nil)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError || !strings.Contains(res.Text, "exploded") {
		t.Fatalf("expected isError result, got %+v", res)
	}
}

func TestServerErrorMessage(t *testing.T) {
	client, _ := newPipedClient(t, []Tool{{Name: "x"}})
	// Force a JSON-RPC error by asking for a tool the server refuses.
	client.tools = nil // irrelevant; direct call below bypasses cached tools
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	// Unknown method: the fake server answers method-not-found.
	err := client.call(ctx, "bogus/method", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "method not found") {
		t.Fatalf("expected method-not-found error, got %v", err)
	}
}
