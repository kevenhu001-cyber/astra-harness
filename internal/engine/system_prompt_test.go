package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildSystemPromptSections(t *testing.T) {
	eng := newTestEngine(t, t.TempDir())
	p := eng.buildSystemPrompt()
	for _, want := range []string{
		"You are Astra, an uncertainty-driven software engineering runtime",
		"## Personality",
		"## Knowledge and uncertainty discipline",
		"## AGENTS.md spec",
		"## Autonomy and persistence",
		"## Responsiveness",
		"## Task execution",
		"## Validating your work",
		"## Presenting your work and final message",
		"## Shell commands",
		"## Search and read",
		"## Editing",
		"## Verification",
		"## MCP tools",
		"## Sandbox and approvals",
		"Current mode: ask",
		"=== COMPILED KNOWLEDGE STATE (query result, not chat history) ===",
		"=== TOOLS ===",
		"- search:",
		"- apply_patch:",
		"- run_command:",
	} {
		if !strings.Contains(p, want) {
			t.Fatalf("system prompt missing %q", want)
		}
	}
	// Sections must appear in a stable order: identity → working rules →
	// tool guidelines → sandbox/approvals → dynamic harness context → tools.
	order := []string{
		"You are Astra",
		"## Personality",
		"## Knowledge and uncertainty discipline",
		"## Task execution",
		"# Tool guidelines",
		"## Sandbox and approvals",
		"=== COMPILED KNOWLEDGE STATE",
		"=== TOOLS ===",
	}
	last := -1
	for _, s := range order {
		i := strings.Index(p, s)
		if i < 0 {
			t.Fatalf("system prompt missing %q", s)
		}
		if i < last {
			t.Fatalf("system prompt section %q out of order (after index %d)", s, last)
		}
		last = i
	}
}

func TestBuildSystemPromptPermissionModes(t *testing.T) {
	cases := []struct {
		mode string
		want string
	}{
		{ModeAsk, "Current mode: ask"},
		{ModeAllow, "Current mode: allow"},
		{ModeDeny, "Current mode: deny"},
	}
	for _, c := range cases {
		eng := newTestEngine(t, t.TempDir())
		eng.Perm.SetMode(c.mode)
		p := eng.buildSystemPrompt()
		if !strings.Contains(p, c.want) {
			t.Fatalf("mode %s: prompt missing %q", c.mode, c.want)
		}
		if !strings.Contains(p, "## Sandbox and approvals") {
			t.Fatalf("mode %s: missing sandbox section", c.mode)
		}
	}

	eng := newTestEngine(t, t.TempDir())
	eng.Perm.SetPlanMode(true)
	p := eng.buildSystemPrompt()
	if !strings.Contains(p, "Current mode: plan") {
		t.Fatalf("plan mode: prompt missing plan instructions:\n%s", p)
	}
}

func TestBuildSystemPromptIncludesProjectInstructions(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("follow repo conventions\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	eng := newTestEngine(t, root)
	p := eng.buildSystemPrompt()
	if !strings.Contains(p, "=== PROJECT INSTRUCTIONS (AGENTS.md) ===") {
		t.Fatalf("missing project instructions section")
	}
	if !strings.Contains(p, "follow repo conventions") {
		t.Fatalf("AGENTS.md content not injected")
	}
}

func TestBuildSystemPromptListsMcpTools(t *testing.T) {
	// The tool catalog section must exist even with no MCP servers; the
	// mcp__ namespace guidance comes from the static prompt.
	eng := newTestEngine(t, t.TempDir())
	p := eng.buildSystemPrompt()
	if !strings.Contains(p, "mcp__<server>__<tool>") {
		t.Fatalf("MCP guidance missing from prompt")
	}
	if strings.Contains(p, "Codex") || strings.Contains(p, "codex") {
		t.Fatalf("system prompt should use Astra branding, got Codex reference")
	}
}
