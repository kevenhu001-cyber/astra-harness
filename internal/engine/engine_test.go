package engine

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/kevenhu001-cyber/astra-harness/internal/llm"
)

func TestDetectTestCommand(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module demo\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := DetectTestCommand(root); got != "go test ./..." {
		t.Fatalf("test command = %q", got)
	}
	if got := DetectBuildCommand(root); got != "go build ./..." {
		t.Fatalf("build command = %q", got)
	}
}

func TestPermissionManagerPlanMode(t *testing.T) {
	p := NewPermissionManager("/tmp/x", ModeAsk, nil)
	p.SetPlanMode(true)
	if ok, _ := p.Check(PermWrite, "/tmp/x/file", "edit", ""); ok {
		t.Fatal("plan mode should block writes")
	}
	if ok, _ := p.Check(PermRead, "/tmp/x/file", "read", ""); !ok {
		t.Fatal("plan mode should allow reads")
	}
	p.SetPlanMode(false)
	p.SetMode(ModeAllow)
	if ok, _ := p.Check(PermWrite, "/tmp/x/file", "edit", ""); !ok {
		t.Fatal("allow mode should permit writes")
	}
}

func TestPermissionManagerDenyAlways(t *testing.T) {
	p := NewPermissionManager("/tmp/x", ModeAsk, func(req PermissionRequest) (PermissionDecision, error) {
		return PermissionDecision{Allowed: false, Always: true}, nil
	})
	ok, err := p.Check(PermExecute, "rm -rf /", "dangerous", "rm -rf /")
	if err != nil || ok {
		t.Fatalf("expected deny, got ok=%v err=%v", ok, err)
	}
	ok, _ = p.Check(PermExecute, "rm -rf /", "dangerous", "rm -rf /")
	if ok {
		t.Fatal("deny-always should persist")
	}
}

func TestSafePathRejectsEscape(t *testing.T) {
	p := NewPermissionManager("/tmp/project", ModeAsk, nil)
	if _, err := p.SafePath("../evil.txt"); err == nil {
		t.Fatal("path escape should be rejected")
	}
	if _, err := p.SafePath("ok/file.txt"); err != nil {
		t.Fatalf("safe path rejected: %v", err)
	}
}

func TestEngineUX(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module demo\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{
		Providers: []ProviderConfig{{
			ID: "test", Type: "openai-compatible", Name: "Test",
			BaseURL: "https://example.invalid/v1", APIKeyEnv: "TEST_API_KEY",
			Models: []string{"test-model"},
		}},
		DefaultProvider: "test", DefaultModel: "test-model",
		PermissionMode: "ask", MaxIterations: 1, TimeoutSeconds: 5,
	}
	t.Setenv("TEST_API_KEY", "stub")
	eng, err := NewEngine(root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Store.Close()

	if got := RelPath(root, filepath.Join(root, "sub", "file.go")); got != "sub/file.go" {
		t.Fatalf("RelPath = %q", got)
	}

	// Seed messages and exercise UndoLastTurn.
	eng.addMessage(llm.RoleUser, "hello")
	eng.addMessage(llm.RoleAssistant, "thinking…")
	eng.addMessage(llm.RoleTool, "ok")
	if got := eng.UndoLastTurn(); got == "nothing to undo" {
		t.Fatalf("undo did nothing: %s", got)
	}
	if len(eng.messages) != 1 || eng.messages[0].Role != llm.RoleUser {
		t.Fatalf("messages = %#v", eng.messages)
	}

	if err := eng.NewSession(); err != nil {
		t.Fatal(err)
	}
	if len(eng.messages) != 0 || eng.session == nil {
		t.Fatalf("NewSession didn't reset; msgs=%d sess=%v", len(eng.messages), eng.session)
	}
}

func TestUpdateProviderPersistsConfig(t *testing.T) {
	root := t.TempDir()
	cfg := &Config{
		Providers: []ProviderConfig{{
			ID: "test", Type: "openai-compatible", Name: "Test",
			BaseURL: "https://example.invalid/v1", APIKeyEnv: "TEST_API_KEY",
			Models: []string{"test-model"},
		}},
		DefaultProvider: "test", DefaultModel: "test-model",
		PermissionMode: "ask", MaxIterations: 1, TimeoutSeconds: 5,
	}
	eng, err := NewEngine(root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Store.Close()

	if err := eng.UpdateProvider("test", "https://new.example/v1", "sk-test-123", "new-model"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, ".astra", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "sk-test-123") || !strings.Contains(string(data), "new-model") {
		t.Fatalf("config did not persist updates:\n%s", data)
	}
	info, err := os.Stat(filepath.Join(root, ".astra", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	// Windows does not enforce POSIX permission bits; the mode assertion is
	// only meaningful on Unix-like systems.
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("config with api key should be 0600, got %v", info.Mode().Perm())
	}
}

func TestBacktrackToUserMessage(t *testing.T) {
	root := t.TempDir()
	cfg := &Config{
		Providers: []ProviderConfig{{
			ID: "test", Type: "openai-compatible", Name: "Test",
			BaseURL: "https://example.invalid/v1", APIKeyEnv: "TEST_API_KEY",
			Models: []string{"test-model"},
		}},
		DefaultProvider: "test", DefaultModel: "test-model",
		PermissionMode: "ask", MaxIterations: 1, TimeoutSeconds: 5,
	}
	t.Setenv("TEST_API_KEY", "stub")
	eng, err := NewEngine(root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Store.Close()
	eng.addMessage(llm.RoleUser, "first")
	eng.addMessage(llm.RoleAssistant, "answer one")
	eng.addMessage(llm.RoleUser, "second")
	eng.addMessage(llm.RoleAssistant, "answer two")
	oldID := eng.SessionID()

	msg, err := eng.BranchBacktrackToUserMessage(1)
	if err != nil {
		t.Fatal(err)
	}
	if msg != "second" {
		t.Fatalf("backtrack message = %q", msg)
	}
	eng.mu.Lock()
	n := len(eng.messages)
	eng.mu.Unlock()
	if n != 3 {
		t.Fatalf("expected 3 messages after backtrack, got %d", n)
	}
	if eng.SessionID() == oldID {
		t.Fatal("branch backtrack should create a new session")
	}
}

func TestStatusLineAndKeymapPersist(t *testing.T) {
	root := t.TempDir()
	cfg := &Config{
		Providers: []ProviderConfig{{
			ID: "test", Type: "openai-compatible", Name: "Test",
			BaseURL: "https://example.invalid/v1", APIKeyEnv: "TEST_API_KEY",
			Models: []string{"test-model"},
		}},
		DefaultProvider: "test", DefaultModel: "test-model",
		PermissionMode: "ask", MaxIterations: 1, TimeoutSeconds: 5,
	}
	t.Setenv("TEST_API_KEY", "stub")
	eng, err := NewEngine(root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Store.Close()

	if err := eng.SetStatusLine([]string{"model", "git-branch"}); err != nil {
		t.Fatal(err)
	}
	if items := eng.StatusLineItems(); len(items) != 2 || items[0] != "model" || items[1] != "git-branch" {
		t.Fatalf("status line = %v", items)
	}
	if err := eng.SetKeymap("transcript", "ctrl+x"); err != nil {
		t.Fatal(err)
	}
	if got := eng.KeymapBinding("transcript"); got != "ctrl+x" {
		t.Fatalf("keymap binding = %q", got)
	}
	if got := eng.KeymapBinding("external_editor"); got != "ctrl+g" {
		t.Fatalf("default keymap binding = %q", got)
	}
}

func TestEngineRename(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module demo\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{
		Providers:       []ProviderConfig{{ID: "test", Type: "openai-compatible", Name: "Test", BaseURL: "https://example.invalid/v1", APIKeyEnv: "TEST_API_KEY", Models: []string{"test-model"}}},
		DefaultProvider: "test", DefaultModel: "test-model",
		PermissionMode: "ask", MaxIterations: 1, TimeoutSeconds: 5,
	}
	t.Setenv("TEST_API_KEY", "stub")
	eng, err := NewEngine(root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Store.Close()
	_ = eng.SessionID()

	if err := eng.RenameSession(""); err == nil {
		t.Fatal("empty session id should be rejected")
	}
	if err := eng.RenameSession("../escape"); err == nil {
		t.Fatal("path-traversal session id should be rejected")
	}
	if err := eng.RenameSession("ses-clean-name"); err != nil {
		t.Fatalf("rename failed: %v", err)
	}
	if eng.SessionID() != "ses-clean-name" {
		t.Fatalf("session id = %q", eng.SessionID())
	}
}
