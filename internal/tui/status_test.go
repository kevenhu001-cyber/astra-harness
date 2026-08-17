package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/kevenhu001-cyber/astra-harness/internal/engine"
)

func TestCodexStatusCard(t *testing.T) {
	a := newTestApp(t)
	_, _ = a.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	a.lastUsage.InputTokens = 700
	a.lastUsage.OutputTokens = 350
	a.totalCost = 0.0123
	a.engine.Config.MaxContextTokens = 272000
	got := strip(codexStatusCard(a))
	for _, want := range []string{
		">_ Astra Harness",
		"Model:",
		"Directory:",
		"Permissions:",
		"Token usage:",
		"1.1K total",
		"700 in + 350 out",
		"Context window:",
		"% left",
		"Estimated cost:",
		"$0.0123",
		"Knowledge:",
		"Session stats:",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("status card missing %q:\n%s", want, got)
		}
	}
	if !strings.HasPrefix(got, "╭") || !strings.HasSuffix(got, "╯") {
		t.Fatalf("status card should be a rounded box:\n%s", got)
	}
}

func TestStatusProgressBar(t *testing.T) {
	got := strip(statusProgressBar(40, 20))
	if strings.Count(got, "█") != 8 || strings.Count(got, "░") != 12 {
		t.Fatalf("progress bar mismatch: %q", got)
	}
	if !strings.HasPrefix(got, "[") || !strings.HasSuffix(got, "]") {
		t.Fatalf("progress bar missing brackets: %q", got)
	}
}

func TestFormatTokensCompact(t *testing.T) {
	cases := map[int]string{412: "412", 1050: "1.1K", 2_300_000: "2.30M"}
	for in, want := range cases {
		if got := formatTokensCompact(in); got != want {
			t.Fatalf("formatTokensCompact(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestRenderPermissionCodexStyle(t *testing.T) {
	a := newTestApp(t)
	_, _ = a.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	a.pendingPerm = &engine.PermissionRequest{
		Kind:        engine.PermExecute,
		Target:      "go",
		Description: "run tests",
		Command:     "go test ./...",
		Risk:        "low",
	}
	got := strip(a.renderPermission())
	for _, want := range []string{
		"Would you like to run the following command?",
		"Reason:",
		"$ go test ./...",
		"1. Yes, proceed",
		"2. Yes, and don't ask again for this command in this session",
		"3. No, continue without running it",
		"4. No, and don't ask again for this command in this session",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("permission prompt missing %q:\n%s", want, got)
		}
	}
}

func TestComposerPromptCodex(t *testing.T) {
	c := newComposer(80)
	if c.ta.Prompt != "› " {
		t.Fatalf("composer prompt should be Codex-style '› ', got %q", c.ta.Prompt)
	}
}

func TestRenderHeaderCodex(t *testing.T) {
	a := newTestApp(t)
	_, _ = a.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	got := strip(a.renderHeader())
	for _, want := range []string{
		">_ Astra Harness",
		"(v0.1.0)",
		"model:",
		"directory:",
		"permissions:",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("header missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "◆") {
		t.Fatalf("header should no longer use the ◆ brand glyph:\n%s", got)
	}
	if got == "" {
		t.Fatal("header rendered empty")
	}
}
