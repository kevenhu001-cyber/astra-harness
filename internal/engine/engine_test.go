package engine

import (
	"os"
	"path/filepath"
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
