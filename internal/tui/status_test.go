package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

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

func TestPermissionSelectionMatchesCodexListFlow(t *testing.T) {
	a := newTestApp(t)
	_, _ = a.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	a.pendingPerm = &engine.PermissionRequest{
		ID: "req_test", Kind: engine.PermExecute, Command: "go test ./...",
	}
	a.mode = modePermission
	if got := strip(a.renderPermission()); !strings.Contains(got, "› 1. Yes, proceed") {
		t.Fatalf("first approval option should be selected:\n%s", got)
	}
	_, _ = a.Update(tea.KeyMsg{Type: tea.KeyDown})
	if a.permissionSelection != 1 {
		t.Fatalf("down should move approval selection, got %d", a.permissionSelection)
	}
	if got := strip(a.renderPermission()); !strings.Contains(got, "› 2. Yes, and don't ask again") {
		t.Fatalf("second approval option should be selected:\n%s", got)
	}
	_, _ = a.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if a.mode != modeChat || a.pendingPerm != nil {
		t.Fatalf("enter should resolve the approval page: mode=%s pending=%v", a.mode, a.pendingPerm)
	}
}

func TestPlanApprovalCodexFlow(t *testing.T) {
	a := newTestApp(t)
	_, _ = a.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	a.planApproval = &planApprovalState{markdown: "1. inspect files\n2. implement changes"}
	a.mode = modePlanApproval
	got := strip(a.renderPlanApproval())
	for _, want := range []string{"Implement this plan?", "Yes, implement this plan", "Yes, clear context and implement", "No, stay in Plan mode"} {
		if !strings.Contains(got, want) {
			t.Fatalf("plan approval page missing %q:\n%s", want, got)
		}
	}
	_, _ = a.Update(tea.KeyMsg{Type: tea.KeyDown})
	if a.planSelection != 1 {
		t.Fatalf("down should select clear-context implementation, got %d", a.planSelection)
	}
	_, _ = a.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if a.mode != modeChat || a.planApproval != nil || a.engine.Perm.IsPlanMode() {
		t.Fatalf("approved plan should leave approval mode: mode=%s pending=%v plan=%v", a.mode, a.planApproval, a.engine.Perm.IsPlanMode())
	}
}

func TestRenderAskUserCodexPrompt(t *testing.T) {
	a := newTestApp(t)
	_, _ = a.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	a.pendingAsk = &askState{id: "ask_test", question: "Which provider should I use?"}
	a.mode = modeAsk
	a.composer.plain = true
	a.composer.ta.Placeholder = "Type your answer"
	got := strip(a.renderAsk())
	for _, want := range []string{"Question from Astra", "Which provider should I use?", "Type your answer", "enter submit", "esc cancel"} {
		if !strings.Contains(got, want) {
			t.Fatalf("ask_user page missing %q:\n%s", want, got)
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
		Preview:     "--- app.go\n+++ app.go\n-old\n+new",
	}
	got := strip(a.renderPermission())
	for _, want := range []string{
		"Would you like to run the following command?",
		"Reason:",
		"$ go test ./...",
		"Proposed changes",
		"+new",
		"1. Yes, proceed",
		"2. Yes, and don't ask again for this command in this session",
		"3. No, continue without running it",
		"4. No, and tell Astra what to do differently",
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

func TestSidebarKeepsMainPaneInsideTerminal(t *testing.T) {
	a := newTestApp(t)
	a.sidebar.visible = true
	_, _ = a.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	out := strip(a.View())
	for _, line := range strings.Split(out, "\n") {
		if lipgloss.Width(line) > 120 {
			t.Fatalf("sidebar layout overflows terminal: width=%d\n%s", lipgloss.Width(line), out)
		}
	}
	if got := lipgloss.Width(strip(a.renderHeader())); got > 94 {
		t.Fatalf("main header should fit the 94-column pane, got %d", got)
	}
}

func TestRenderHeaderCodex(t *testing.T) {
	a := newTestApp(t)
	_, _ = a.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	got := strip(a.renderHeader())
	for _, want := range []string{
		">_ Astra Harness (v0.1.0)",
		"model:",
		"directory:",
		"permissions:",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("header missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "██") {
		t.Fatalf("header should use the compact Codex session title instead of a large logo:\n%s", got)
	}
	if got == "" {
		t.Fatal("header rendered empty")
	}
}
