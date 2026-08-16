package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kevenhu001-cyber/astra-harness/internal/core"
	"github.com/kevenhu001-cyber/astra-harness/internal/llm"
)

func newTestEngine(t *testing.T, root string) *Engine {
	t.Helper()
	cfg := &Config{
		Providers: []ProviderConfig{{
			ID: "test", Type: "openai-compatible", Name: "Test",
			BaseURL: "https://example.invalid/v1", APIKeyEnv: "TEST_API_KEY",
			Models: []string{"test-model"},
		}},
		DefaultProvider: "test", DefaultModel: "test-model",
		PermissionMode: ModeAsk, MaxIterations: 1, TimeoutSeconds: 5,
	}
	t.Setenv("TEST_API_KEY", "stub")
	eng, err := NewEngine(root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eng.Close() })
	return eng
}

// TestLoadProjectInstructions mirrors Codex agents_md.rs: AGENTS.override.md
// takes priority over AGENTS.md in the same directory, both are collected from
// the project root down to the cwd, and the total is capped.
func TestLoadProjectInstructions(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "pkg", "core")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("root instructions\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "AGENTS.md"), []byte("sub instructions\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "AGENTS.override.md"), []byte("override beats local\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	eng := newTestEngine(t, root)
	eng.Root = root

	// Simulate running from the subdirectory.
	oldwd, _ := os.Getwd()
	if err := os.Chdir(sub); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(oldwd)

	docs := eng.LoadProjectInstructions()
	if !strings.Contains(docs, "root instructions") {
		t.Fatalf("missing root AGENTS.md:\n%s", docs)
	}
	if !strings.Contains(docs, "override beats local") {
		t.Fatalf("AGENTS.override.md should take priority over local AGENTS.md:\n%s", docs)
	}
	if strings.Contains(docs, "sub instructions") {
		t.Fatalf("local AGENTS.md should be shadowed by AGENTS.override.md:\n%s", docs)
	}
	if !strings.Contains(docs, "--- project-doc ---") {
		t.Fatalf("docs should be joined by the separator:\n%s", docs)
	}
	// Root instructions must come first.
	if !strings.HasPrefix(docs, "# AGENTS.md") {
		t.Fatalf("root doc should be first:\n%s", docs)
	}
}

func TestLoadProjectInstructionsByteCap(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("0123456789abcdef"), 0o644); err != nil {
		t.Fatal(err)
	}
	eng := newTestEngine(t, root)
	eng.Config.MaxProjectDocBytes = 24
	oldwd, _ := os.Getwd()
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(oldwd)
	docs := eng.LoadProjectInstructions()
	if len(docs) > 24 {
		t.Fatalf("expected doc capped at 24 bytes, got %d: %q", len(docs), docs)
	}
}

func TestLoadProjectInstructionsOutsideRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("root only instructions\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	eng := newTestEngine(t, root)
	// cwd is the test package dir, outside the temp project root: we must fall
	// back to scanning the project root itself.
	docs := eng.LoadProjectInstructions()
	if !strings.Contains(docs, "root only instructions") {
		t.Fatalf("expected fallback to project root scan:\n%s", docs)
	}
}

// TestShouldAutoCompact verifies the Codex-style token-budget trigger: once
// the estimated context crosses 80% of MaxContextTokens, compaction fires.
func TestShouldAutoCompact(t *testing.T) {
	root := t.TempDir()
	eng := newTestEngine(t, root)
	eng.Config.MaxContextTokens = 4000 // tiny budget

	if eng.shouldAutoCompact() {
		t.Fatal("empty context should not compact")
	}
	// ~5000 chars ≈ 1250 tokens; system prompt adds ~75; budget 4000 * 0.8 = 3200.
	big := strings.Repeat("x", 20000)
	eng.addMessage(llm.RoleUser, big)
	if !eng.shouldAutoCompact() {
		t.Fatal("expected auto-compact trigger above 80% budget")
	}
	// And the trigger clears once compacted.
	eng.Config.MaxContextTokens = 1 << 30
	if eng.shouldAutoCompact() {
		t.Fatal("huge budget should not compact")
	}
}

// TestPermissionEnforcesWritesOnEdit verifies the WRITE permission now gates
// edit_file/write_file (Codex permission profiles: read-only blocks writes).
func TestPermissionEnforcesWritesOnEdit(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "file.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	eng := newTestEngine(t, root)
	eng.Perm.SetMode(ModeDeny)

	res := eng.ExecuteTool(context.Background(), "edit_file", `{"path":"file.go","old_string":"package main","new_string":"package x"}`)
	if res.Success {
		t.Fatal("deny mode should block edit_file")
	}
	res = eng.ExecuteTool(context.Background(), "write_file", `{"path":"new.go","content":"package new"}`)
	if res.Success {
		t.Fatal("deny mode should block write_file")
	}

	eng.Perm.SetMode(ModeAllow)
	res = eng.ExecuteTool(context.Background(), "edit_file", `{"path":"file.go","old_string":"package main","new_string":"package x"}`)
	if !res.Success {
		t.Fatalf("allow mode should permit edit_file: %s", res.Output)
	}
}

func TestIsRetryable(t *testing.T) {
	rateLimit := llm.NewHTTPStatusError("openai", 429, "rate limited")
	if !isRetryable(retryableError{rateLimit}) {
		t.Fatal("429 should be retryable")
	}
	serverErr := llm.NewHTTPStatusError("openai", 503, "overloaded")
	if !isRetryable(retryableError{serverErr}) {
		t.Fatal("5xx should be retryable")
	}
	authErr := llm.NewHTTPStatusError("openai", 401, "bad key")
	if isRetryable(retryableError{authErr}) {
		t.Fatal("401 should not be retryable")
	}
	badReq := llm.NewHTTPStatusError("openai", 400, "bad request")
	if isRetryable(retryableError{badReq}) {
		t.Fatal("400 should not be retryable")
	}
	network := errors.New("Post \"https://x\": dial tcp: connection refused")
	if !isRetryable(retryableError{network}) {
		t.Fatal("network errors should be retryable")
	}
	if isRetryable(errors.New("plain failure")) {
		t.Fatal("non-retryable errors should not be retried")
	}
}

// TestCallModelRetriesTransientErrors verifies the backoff retry loop: a 503
// then a 200 should succeed after exactly two requests.
func TestCallModelRetriesTransientErrors(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprint(w, "overloaded")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	root := t.TempDir()
	cfg := &Config{
		Providers: []ProviderConfig{{
			ID: "mock", Type: "openai-compatible", Name: "Mock",
			BaseURL: srv.URL, APIKeyEnv: "MOCK_KEY", Models: []string{"mock-model"},
		}},
		DefaultProvider: "mock", DefaultModel: "mock-model",
		PermissionMode: ModeAllow, MaxIterations: 1, TimeoutSeconds: 10,
	}
	t.Setenv("MOCK_KEY", "k")
	eng, err := NewEngine(root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	content, _, _, err := eng.callModel(context.Background())
	if err != nil {
		t.Fatalf("callModel should have recovered: %v", err)
	}
	if strings.TrimSpace(content) != "ok" {
		t.Fatalf("content = %q", content)
	}
	if requests.Load() != 2 {
		t.Fatalf("expected 2 requests (1 failed + 1 retry), got %d", requests.Load())
	}
}

// TestCallModelNoRetryOnBadRequest verifies deterministic failures are not retried.
func TestCallModelNoRetryOnBadRequest(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, "bad request")
	}))
	defer srv.Close()

	root := t.TempDir()
	cfg := &Config{
		Providers: []ProviderConfig{{
			ID: "mock", Type: "openai-compatible", Name: "Mock",
			BaseURL: srv.URL, APIKeyEnv: "MOCK_KEY", Models: []string{"mock-model"},
		}},
		DefaultProvider: "mock", DefaultModel: "mock-model",
		PermissionMode: ModeAllow, MaxIterations: 1, TimeoutSeconds: 10,
	}
	t.Setenv("MOCK_KEY", "k")
	eng, err := NewEngine(root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	if _, _, _, err := eng.callModel(context.Background()); err == nil {
		t.Fatal("expected error for 400")
	}
	if requests.Load() != 1 {
		t.Fatalf("expected no retry on 400, got %d requests", requests.Load())
	}
}

// TestReconcileClaimsAfterEdit drives the full evidence-invalidation loop in
// a real git repo: a VERIFIED claim at state A turns STALE after the working
// tree changes, and a fresh verify re-establishes it at the new state.
func TestReconcileClaimsAfterEdit(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitInit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git unavailable: %v: %s", err, out)
		}
	}
	gitInit("init", "-q")
	gitInit("add", "-A")
	gitInit("commit", "-q", "-m", "init")

	eng := newTestEngine(t, root)
	stateA := eng.Git.StateHash()

	// Simulate a verification that succeeded at state A.
	ev := &core.Evidence{
		ID: core.NewID("ev"), Kind: core.EvidenceTestResult,
		Source: "go test ./...", Content: "PASS", Status: "VALID",
		CodeState: stateA, Confidence: 0.9,
		CreatedAt: time.Now().UTC(),
	}
	if err := eng.Store.AddEvidence(ev); err != nil {
		t.Fatal(err)
	}
	cl := &core.Claim{
		ID: core.NewID("clm"), Subject: "test suite", Predicate: "passes",
		Object: "go test ./...", ClaimType: "TEST_RESULT",
		Status: core.ClaimVerified, Confidence: 0.9, CodeState: stateA,
		EvidenceIDs: []string{ev.ID}, Source: "verify",
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := eng.Store.AddClaim(cl); err != nil {
		t.Fatal(err)
	}

	// Before any change: nothing is stale.
	if n := eng.ReconcileClaims(); n != 0 {
		t.Fatalf("expected no stale records at initial state, got %d", n)
	}

	// Change the working tree → code state B.
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stateB := eng.Git.StateHash()
	if stateA == stateB {
		t.Fatal("state hash should change after edit")
	}

	n := eng.ReconcileClaims()
	if n < 2 {
		t.Fatalf("expected evidence + claim flagged STALE, got %d", n)
	}
	for _, e := range eng.Store.State.Evidence {
		if e.ID == ev.ID && e.Status != core.EvidenceStale {
			t.Fatalf("evidence status = %s, want %s", e.Status, core.EvidenceStale)
		}
	}
	for _, c := range eng.Store.State.Claims {
		if c.ID == cl.ID && c.Status != core.ClaimStale {
			t.Fatalf("claim status = %s, want %s", c.Status, core.ClaimStale)
		}
	}

	// Reconcile again: idempotent, no new events.
	if n := eng.ReconcileClaims(); n != 0 {
		t.Fatalf("reconcile should be idempotent, got %d new stale", n)
	}
}

// TestCompilerMemoryInjection verifies cross-session memory activation: a
// goal-relevant verified claim from a prior session ranks first in the
// compiled state, and the Run-start memory summary surfaces it.
func TestCompilerMemoryInjection(t *testing.T) {
	root := t.TempDir()
	eng := newTestEngine(t, root)
	now := time.Now()
	eng.SetGoal("fix jwt auth", nil)
	if err := eng.Store.AddClaim(&core.Claim{
		ID: "c1", Subject: "jwt auth middleware", Predicate: "works", Object: "in api",
		Status: core.ClaimVerified, Confidence: 0.9, Source: "verify",
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := eng.Store.AddClaim(&core.Claim{
		ID: "c2", Subject: "database layer", Predicate: "uses", Object: "gorm",
		Status: core.ClaimVerified, Confidence: 0.9, Source: "verify",
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	out := eng.CompilerOutput()
	iJwt := strings.Index(out, "jwt auth middleware")
	iDb := strings.Index(out, "database layer")
	if iJwt < 0 || iDb < 0 {
		t.Fatalf("claims missing from compiled state:\n%s", out)
	}
	if iJwt > iDb {
		t.Fatalf("goal-relevant claim should rank first:\n%s", out)
	}

	summary := eng.memorySummary()
	if !strings.Contains(summary, "2 claim(s) (2 verified)") || !strings.Contains(summary, "jwt auth middleware") {
		t.Fatalf("memory summary missing activation info: %s", summary)
	}
}

var _ = io.Discard
