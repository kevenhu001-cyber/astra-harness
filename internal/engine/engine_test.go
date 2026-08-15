package engine

import (
	"os"
	"path/filepath"
	"testing"
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
