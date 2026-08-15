package knowledge

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractSymbolsGo(t *testing.T) {
	src := "package main\n\nfunc hello() {}\n\ntype Server struct{}\n\nvar count = 0\n"
	syms := ExtractSymbols("main.go", src)
	names := map[string]bool{}
	for _, s := range syms {
		names[s.Name] = true
	}
	for _, want := range []string{"hello", "Server", "count"} {
		if !names[want] {
			t.Fatalf("missing symbol %s in %+v", want, syms)
		}
	}
}

func TestIndexBuildAndFallbackSearch(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"src/main.go":        "package main\n\nfunc runServer() {}\n",
		"src/server_test.go": "package main\n\nfunc TestRunServer(t) {}\n",
		"README.md":          "# demo\n",
	}
	for p, c := range files {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ix := NewIndex(root)
	if err := ix.Build(); err != nil {
		t.Fatal(err)
	}
	if len(ix.Files) != 3 {
		t.Fatalf("expected 3 files, got %d", len(ix.Files))
	}
	tests := ix.TestFiles()
	if len(tests) != 1 || !strings.Contains(tests[0], "server_test.go") {
		t.Fatalf("test files = %v", tests)
	}
	results, err := ix.fallbackSearch(context.Background(), "runServer", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 || !strings.Contains(results[0].Content, "runServer") {
		t.Fatalf("fallback search results = %+v", results)
	}
	if hits := ix.FindSymbol("runServer"); len(hits) != 1 {
		t.Fatalf("FindSymbol = %+v", hits)
	}
}

func TestIsTestFile(t *testing.T) {
	for _, p := range []string{"a_test.go", "tests/util.py", "x.spec.ts", "test_main.py"} {
		if !isTestFile(p) {
			t.Fatalf("expected %s to be a test file", p)
		}
	}
	if isTestFile("src/main.go") {
		t.Fatal("src/main.go should not be a test file")
	}
}
