package engine

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unicode/utf16"
)

// hookDenyCommand prints text to stderr and exits non-zero on both shells.
func hookDenyCommand(text string) string {
	if runtime.GOOS == "windows" {
		return "echo " + text + " 1>&2 && exit /b 1"
	}
	return "echo '" + text + "' 1>&2 && exit 1"
}

// hookEchoExit prints text then exits non-zero on both shells.
func hookEchoExit(text string) string {
	if runtime.GOOS == "windows" {
		return "echo " + text + " && exit /b 1"
	}
	return "echo '" + text + "' && exit 1"
}

// hookCaptureStdin returns a shell command that writes stdin to path.
func hookCaptureStdin(path string) string {
	if runtime.GOOS == "windows" {
		// -EncodedCommand avoids cmd/PowerShell argument parsing eating the
		// "$" variables; the script reads the full stdin stream (including a
		// payload without a trailing newline) and writes exact bytes.
		script := "$p='" + path + "'; [IO.File]::WriteAllText($p, [Console]::In.ReadToEnd())"
		var raw []byte
		for _, u := range utf16.Encode([]rune(script)) {
			raw = append(raw, byte(u), byte(u>>8))
		}
		encoded := base64.StdEncoding.EncodeToString(raw)
		return "powershell -NoProfile -NonInteractive -EncodedCommand " + encoded
	}
	return "cat > " + quoteShell(path)
}

// hookNoop returns a command that succeeds on both shells.
func hookNoop() string {
	return "cd ."
}

func newTestEngineWithHooks(t *testing.T, root string, hooks []HookConfig) *Engine {
	t.Helper()
	cfg := &Config{
		Providers: []ProviderConfig{{
			ID: "test", Type: "openai-compatible", Name: "Test",
			BaseURL: "https://example.invalid/v1", APIKeyEnv: "TEST_API_KEY",
			Models: []string{"test-model"},
		}},
		DefaultProvider: "test", DefaultModel: "test-model",
		PermissionMode: ModeAllow, MaxIterations: 1, TimeoutSeconds: 5,
		Hooks: hooks,
	}
	t.Setenv("TEST_API_KEY", "stub")
	eng, err := NewEngine(root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eng.Close() })
	return eng
}

// TestHookPreToolUseDenies verifies a non-zero-exit PreToolUse hook blocks the
// tool call and surfaces the hook output as the reason.
func TestHookPreToolUseDenies(t *testing.T) {
	eng := newTestEngineWithHooks(t, t.TempDir(), []HookConfig{{
		Event: "PreToolUse", Command: hookDenyCommand("no run_command for you"),
	}})
	res := eng.ExecuteTool(context.Background(), "run_command", `{"command":"echo hi"}`)
	if res.Success {
		t.Fatal("hook should deny the tool call")
	}
	if !strings.Contains(res.Output, "hook denied") || !strings.Contains(res.Output, "no run_command for you") {
		t.Fatalf("denial message missing hook output: %s", res.Output)
	}
}

// TestHookPreToolUseAllows verifies exit 0 lets the call through.
func TestHookPreToolUseAllows(t *testing.T) {
	eng := newTestEngineWithHooks(t, t.TempDir(), []HookConfig{{
		Event: "PreToolUse", Command: hookNoop(),
	}})
	res := eng.ExecuteTool(context.Background(), "run_command", `{"command":"echo hi"}`)
	if !res.Success {
		t.Fatalf("hook should allow: %s", res.Output)
	}
	if !strings.Contains(res.Output, "hi") {
		t.Fatalf("command output missing: %s", res.Output)
	}
}

// TestHookToolFilter verifies the tools filter scopes a hook to specific tools.
func TestHookToolFilter(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "f.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	eng := newTestEngineWithHooks(t, root, []HookConfig{{
		Event: "PreToolUse", Tools: []string{"edit_file"}, Command: "exit 1",
	}})
	// run_command is not filtered → allowed.
	res := eng.ExecuteTool(context.Background(), "run_command", `{"command":"echo ok"}`)
	if !res.Success {
		t.Fatalf("run_command should not be affected by edit_file hook: %s", res.Output)
	}
	// edit_file is filtered → denied.
	res = eng.ExecuteTool(context.Background(), "edit_file",
		`{"path":"f.go","old_string":"package main","new_string":"package x"}`)
	if res.Success {
		t.Fatal("edit_file should be denied by its hook")
	}
}

// TestHookPostToolUseReceivesPayload verifies post hooks see the tool result
// on stdin (tool name, success, output).
func TestHookPostToolUseReceivesPayload(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, "hook_payload.json")
	// Use printf to write stdin to the file; the hook itself always succeeds.
	eng := newTestEngineWithHooks(t, root, []HookConfig{{
		Event:   "PostToolUse",
		Command: hookCaptureStdin(out),
	}})
	res := eng.ExecuteTool(context.Background(), "run_command", `{"command":"echo hello"}`)
	if !res.Success {
		t.Fatalf("tool failed: %s", res.Output)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("post hook did not capture payload: %v", err)
	}
	var payload struct {
		Tool    string `json:"tool"`
		Success bool   `json:"success"`
		Output  string `json:"output"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("invalid hook payload %q: %v", data, err)
	}
	if payload.Tool != "run_command" || !payload.Success || !strings.Contains(payload.Output, "hello") {
		t.Fatalf("payload = %+v", payload)
	}
}

// TestHookPreCompactBlocks verifies PreCompact can abort compaction.
func TestHookPreCompactBlocks(t *testing.T) {
	eng := newTestEngineWithHooks(t, t.TempDir(), []HookConfig{{
		Event: "PreCompact", Command: hookEchoExit("do not compact"),
	}})
	eng.addMessage("user", "seed one")
	eng.addMessage("assistant", "seed two")
	eng.addMessage("tool", "seed three")
	got := eng.Compact()
	if !strings.Contains(got, "blocked by hook") || !strings.Contains(got, "do not compact") {
		t.Fatalf("expected blocked compaction, got: %s", got)
	}
	if len(eng.messages) != 3 {
		t.Fatalf("messages should be untouched after blocked compact, got %d", len(eng.messages))
	}
}

func quoteShell(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
