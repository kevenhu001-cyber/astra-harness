package engine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParsePatch(t *testing.T) {
	patch := "*** Begin Patch\n" +
		"*** Update File: src/main.go\n" +
		"@@\n" +
		"-import \"fmt\"\n" +
		"+import \"io\"\n" +
		" \n" +
		" func main() {\n" +
		"*** Update File: src/util.go\n" +
		"@@ func helper() {\n" +
		"-\treturn 1\n" +
		"+\treturn 2\n" +
		"*** Add File: src/new.go\n" +
		"+package src\n" +
		"+\n" +
		"+func added() {}\n" +
		"*** Delete File: src/old.go\n" +
		"*** End Patch"
	hunks, err := parsePatch(patch)
	if err != nil {
		t.Fatal(err)
	}
	if len(hunks) != 4 {
		t.Fatalf("expected 4 hunks, got %d", len(hunks))
	}
	u := hunks[0]
	if u.kind != "update" || u.path != "src/main.go" {
		t.Fatalf("hunk 0 = %+v", u)
	}
	if len(u.chunks) != 1 {
		t.Fatalf("hunk 0 chunks = %+v", u.chunks)
	}
	c := u.chunks[0]
	if len(c.oldLines) != 3 || c.oldLines[0] != "import \"fmt\"" || c.oldLines[2] != "func main() {" {
		t.Fatalf("hunk 0 oldLines = %+v", c.oldLines)
	}
	if len(c.newLines) != 3 || c.newLines[0] != "import \"io\"" {
		t.Fatalf("hunk 0 newLines = %+v", c.newLines)
	}
	if hunks[2].kind != "add" || hunks[2].path != "src/new.go" || len(hunks[2].content) != 3 {
		t.Fatalf("add hunk = %+v", hunks[2])
	}
	if hunks[3].kind != "delete" || hunks[3].path != "src/old.go" {
		t.Fatalf("delete hunk = %+v", hunks[3])
	}
}

func TestParsePatchRejectsBadBoundaries(t *testing.T) {
	if _, err := parsePatch("*** Update File: a.go\n@@\n-x\n+y"); err == nil {
		t.Fatal("patch without Begin marker should fail")
	}
	if _, err := parsePatch("*** Begin Patch\n*** End Patch"); err == nil {
		t.Fatal("patch with no hunks should fail")
	}
	if _, err := parsePatch("*** Begin Patch\n@@\n*** End Patch"); err == nil {
		t.Fatal("orphan @@ should fail")
	}
}

func TestApplyPatchUpdateAndAdd(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"),
		[]byte("package main\n\nimport \"fmt\"\n\nfunc run() {\n\tfmt.Println(1)\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	eng := newTestEngine(t, root)
	eng.Perm.SetMode(ModeAllow)

	patch := "*** Begin Patch\n" +
		"*** Update File: main.go\n" +
		"@@\n" +
		"-import \"fmt\"\n" +
		"+import \"os\"\n" +
		"@@ func run() {\n" +
		"-\tfmt.Println(1)\n" +
		"+\tfmt.Println(2)\n" +
		"*** Add File: extra.go\n" +
		"+package main\n" +
		"+\n" +
		"+const extra = true\n" +
		"*** End Patch"
	args, _ := json.Marshal(map[string]any{"patch": patch})
	res := eng.ExecuteTool(context.Background(), "apply_patch", string(args))
	if !res.Success {
		t.Fatalf("apply_patch failed: %s", res.Output)
	}
	got, _ := os.ReadFile(filepath.Join(root, "main.go"))
	content := string(got)
	if strings.Contains(content, `"fmt"`) || !strings.Contains(content, `"os"`) {
		t.Fatalf("main.go not updated:\n%s", content)
	}
	if !strings.Contains(content, "fmt.Println(2)") || strings.Contains(content, "fmt.Println(1)") {
		t.Fatalf("second hunk not applied:\n%s", content)
	}
	extra, err := os.ReadFile(filepath.Join(root, "extra.go"))
	if err != nil {
		t.Fatalf("extra.go not created: %v", err)
	}
	if !strings.Contains(string(extra), "const extra = true") {
		t.Fatalf("extra.go content:\n%s", extra)
	}
}

func TestApplyPatchAppendAtEnd(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"),
		[]byte("package main\n\nfunc run() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	eng := newTestEngine(t, root)
	eng.Perm.SetMode(ModeAllow)

	// Pure insertion with no @@ anchor appends at end of file (Codex semantics).
	patch := "*** Begin Patch\n" +
		"*** Update File: main.go\n" +
		"@@\n" +
		"+func extra() {}\n" +
		"*** End Patch"
	args, _ := json.Marshal(map[string]any{"patch": patch})
	res := eng.ExecuteTool(context.Background(), "apply_patch", string(args))
	if !res.Success {
		t.Fatalf("apply_patch failed: %s", res.Output)
	}
	got, _ := os.ReadFile(filepath.Join(root, "main.go"))
	content := string(got)
	if !strings.Contains(content, "func run() {}\nfunc extra() {}") {
		t.Fatalf("expected append at end:\n%s", content)
	}
}

func TestApplyPatchDeleteAndMove(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package main\n\nfunc a() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "b.go"), []byte("package main\n\nfunc b() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	eng := newTestEngine(t, root)
	eng.Perm.SetMode(ModeAllow)

	patch := "*** Begin Patch\n" +
		"*** Delete File: a.go\n" +
		"*** Update File: b.go\n" +
		"*** Move to: c.go\n" +
		"@@\n" +
		" func b() {}\n" +
		"*** End Patch"
	args, _ := json.Marshal(map[string]any{"patch": patch})
	res := eng.ExecuteTool(context.Background(), "apply_patch", string(args))
	if !res.Success {
		t.Fatalf("apply_patch failed: %s", res.Output)
	}
	if _, err := os.Stat(filepath.Join(root, "a.go")); !os.IsNotExist(err) {
		t.Fatal("a.go should be deleted")
	}
	if _, err := os.Stat(filepath.Join(root, "b.go")); !os.IsNotExist(err) {
		t.Fatal("b.go should be renamed away")
	}
	content, err := os.ReadFile(filepath.Join(root, "c.go"))
	if err != nil {
		t.Fatalf("c.go not created: %v", err)
	}
	if !strings.Contains(string(content), "func b()") {
		t.Fatalf("c.go content:\n%s", content)
	}
}

func TestApplyPatchHunkMismatch(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc run() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	eng := newTestEngine(t, root)
	eng.Perm.SetMode(ModeAllow)

	patch := "*** Begin Patch\n" +
		"*** Update File: main.go\n" +
		"@@\n" +
		"-this line does not exist\n" +
		"+replacement\n" +
		"*** End Patch"
	args, _ := json.Marshal(map[string]any{"patch": patch})
	res := eng.ExecuteTool(context.Background(), "apply_patch", string(args))
	if res.Success {
		t.Fatal("mismatched hunk should fail")
	}
	if !strings.Contains(res.Output, "did not match") {
		t.Fatalf("expected mismatch message, got: %s", res.Output)
	}
	got, _ := os.ReadFile(filepath.Join(root, "main.go"))
	if !strings.Contains(string(got), "func run()") {
		t.Fatal("file should be unchanged after failed patch")
	}
}

func TestApplyPatchPermissionDeny(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	eng := newTestEngine(t, root)
	eng.Perm.SetMode(ModeDeny)

	patch := "*** Begin Patch\n" +
		"*** Update File: main.go\n" +
		"@@\n" +
		"-package main\n" +
		"+package x\n" +
		"*** End Patch"
	args, _ := json.Marshal(map[string]any{"patch": patch})
	res := eng.ExecuteTool(context.Background(), "apply_patch", string(args))
	if res.Success {
		t.Fatal("deny mode should block apply_patch")
	}
	if !strings.Contains(res.Output, "denied") {
		t.Fatalf("expected denial message, got: %s", res.Output)
	}
}
