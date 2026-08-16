package engine

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

// TestMcpHelperProcess re-executes the test binary as a fake MCP stdio server
// (the standard Go helper-process pattern). It serves the initialize
// handshake, tools/list and tools/call over newline-delimited JSON.
func TestMcpHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_MCP_HELPER") != "1" {
		return
	}
	sc := bufio.NewScanner(os.Stdin)
	write := func(msg map[string]any) {
		data, _ := json.Marshal(msg)
		os.Stdout.Write(append(data, '\n'))
	}
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var req struct {
			ID     *int            `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal([]byte(line), &req) != nil {
			continue
		}
		if req.ID == nil {
			continue
		}
		switch req.Method {
		case "initialize":
			write(map[string]any{"jsonrpc": "2.0", "id": *req.ID, "result": map[string]any{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "fake", "version": "1.0"},
			}})
		case "tools/list":
			write(map[string]any{"jsonrpc": "2.0", "id": *req.ID, "result": map[string]any{
				"tools": []map[string]any{
					{
						"name": "greet", "description": "greets a name",
						"inputSchema": map[string]any{
							"type":       "object",
							"properties": map[string]any{"name": map[string]any{"type": "string"}},
						},
					},
					{
						"name": "secret", "description": "dangerous tool",
						"inputSchema": map[string]any{"type": "object"},
					},
				},
			}})
		case "tools/call":
			var params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}
			_ = json.Unmarshal(req.Params, &params)
			name, _ := params.Arguments["name"].(string)
			write(map[string]any{"jsonrpc": "2.0", "id": *req.ID, "result": map[string]any{
				"content": []map[string]any{{"type": "text", "text": "hello " + name}},
				"isError": false,
			}})
		default:
			write(map[string]any{"jsonrpc": "2.0", "id": *req.ID,
				"error": map[string]any{"code": -32601, "message": "method not found"}})
		}
	}
	os.Exit(0)
}

// TestEngineMcpIntegration wires a real (self-executing) MCP server through
// the engine: config → startup handshake → tools/list → tool exposure →
// permission-gated dispatch → tools/call.
func TestEngineMcpIntegration(t *testing.T) {
	root := t.TempDir()
	helper, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cfg := &Config{
		Providers: []ProviderConfig{{
			ID: "test", Type: "openai-compatible", Name: "Test",
			BaseURL: "https://example.invalid/v1", APIKeyEnv: "TEST_API_KEY",
			Models: []string{"test-model"},
		}},
		DefaultProvider: "test", DefaultModel: "test-model",
		PermissionMode: ModeAllow, MaxIterations: 1, TimeoutSeconds: 5,
		McpServers: []McpServerConfig{{
			ID: "fake", Command: helper,
			Args: []string{"-test.run=TestMcpHelperProcess"},
			Env:  map[string]string{"GO_WANT_MCP_HELPER": "1"},
		}},
	}
	t.Setenv("TEST_API_KEY", "stub")
	eng, err := NewEngine(root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	// The MCP tool must be exposed under the mcp__<server>__<tool> namespace.
	var found bool
	for _, def := range eng.AllToolDefs() {
		if def.Name == "mcp__fake__greet" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("mcp__fake__greet not exposed; got %d defs", len(eng.AllToolDefs()))
	}

	// Dispatch through ExecuteTool (allow mode auto-approves the EXECUTE gate).
	res := eng.ExecuteTool(context.Background(), "mcp__fake__greet", `{"name":"world"}`)
	if !res.Success {
		t.Fatalf("mcp call failed: %s", res.Output)
	}
	if !strings.Contains(res.Output, "hello world") {
		t.Fatalf("unexpected output: %q", res.Output)
	}

	// Unknown server id must fail cleanly.
	res = eng.ExecuteTool(context.Background(), "mcp__nope__greet", `{}`)
	if res.Success {
		t.Fatal("unknown server should fail")
	}
}

// TestEngineMcpPermissionGate verifies MCP calls honor the EXECUTE permission
// (deny mode blocks them like any other execution).
func TestEngineMcpPermissionGate(t *testing.T) {
	root := t.TempDir()
	helper, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cfg := &Config{
		Providers: []ProviderConfig{{
			ID: "test", Type: "openai-compatible", Name: "Test",
			BaseURL: "https://example.invalid/v1", APIKeyEnv: "TEST_API_KEY",
			Models: []string{"test-model"},
		}},
		DefaultProvider: "test", DefaultModel: "test-model",
		PermissionMode: ModeDeny, MaxIterations: 1, TimeoutSeconds: 5,
		McpServers: []McpServerConfig{{
			ID: "fake", Command: helper,
			Args: []string{"-test.run=TestMcpHelperProcess"},
			Env:  map[string]string{"GO_WANT_MCP_HELPER": "1"},
		}},
	}
	t.Setenv("TEST_API_KEY", "stub")
	eng, err := NewEngine(root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	res := eng.ExecuteTool(context.Background(), "mcp__fake__greet", `{"name":"world"}`)
	if res.Success {
		t.Fatal("deny mode should block mcp calls")
	}
	if !strings.Contains(res.Output, "denied") {
		t.Fatalf("expected denial message, got: %q", res.Output)
	}
	_ = fmt.Sprint // keep fmt imported for helper process use
}

// TestEngineMcpPerToolDisabled verifies per-tool config (Codex
// [mcp_servers.<name>.tools.<tool>].disabled): the disabled tool is not
// exposed to the model and dispatch is refused.
func TestEngineMcpPerToolDisabled(t *testing.T) {
	root := t.TempDir()
	helper, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cfg := &Config{
		Providers: []ProviderConfig{{
			ID: "test", Type: "openai-compatible", Name: "Test",
			BaseURL: "https://example.invalid/v1", APIKeyEnv: "TEST_API_KEY",
			Models: []string{"test-model"},
		}},
		DefaultProvider: "test", DefaultModel: "test-model",
		PermissionMode: ModeAllow, MaxIterations: 1, TimeoutSeconds: 5,
		McpServers: []McpServerConfig{{
			ID: "fake", Command: helper,
			Args: []string{"-test.run=TestMcpHelperProcess"},
			Env:  map[string]string{"GO_WANT_MCP_HELPER": "1"},
			Tools: map[string]McpToolConfig{
				"secret": {Disabled: true},
			},
		}},
	}
	t.Setenv("TEST_API_KEY", "stub")
	eng, err := NewEngine(root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	// greet is exposed; secret is filtered out.
	var exposedGreet, exposedSecret bool
	for _, def := range eng.AllToolDefs() {
		switch def.Name {
		case "mcp__fake__greet":
			exposedGreet = true
		case "mcp__fake__secret":
			exposedSecret = true
		}
	}
	if !exposedGreet {
		t.Fatal("greet should be exposed")
	}
	if exposedSecret {
		t.Fatal("disabled tool must not be exposed")
	}

	// Dispatch of the disabled tool is refused even by direct call.
	res := eng.ExecuteTool(context.Background(), "mcp__fake__secret", `{}`)
	if res.Success || !strings.Contains(res.Output, "disabled by config") {
		t.Fatalf("expected disabled-by-config refusal, got success=%v out=%q", res.Success, res.Output)
	}
}
