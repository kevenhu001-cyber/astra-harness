package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/kevenhu001-cyber/astra-harness/internal/engine"
	"github.com/kevenhu001-cyber/astra-harness/internal/llm"
)

// helper: build a tiny app with a mock-like engine config (no providers).
func newTestApp(t *testing.T) *app {
	t.Helper()
	root := t.TempDir()
	cfg := &engine.Config{
		Providers: []engine.ProviderConfig{
			{
				ID:        "test",
				Type:      "openai-compatible",
				Name:      "TestProvider",
				BaseURL:   "https://example.invalid/v1",
				APIKeyEnv: "TEST_API_KEY",
				Models:    []string{"test-model"},
			},
		},
		DefaultProvider: "test",
		DefaultModel:    "test-model",
		PermissionMode:  "ask",
		MaxIterations:   1,
		TimeoutSeconds:  10,
	}
	t.Setenv("TEST_API_KEY", "stub")
	eng, err := engine.NewEngine(root, cfg)
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	t.Cleanup(func() { _ = eng.Store.Close() })
	return NewApp(root, cfg, eng)
}

func TestAppRender(t *testing.T) {
	a := newTestApp(t)
	a.composer.SetWidth(80)
	_, _ = a.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	a.refreshFileCandidates()
	a.refreshViewport()
	out := a.View()
	if !strings.Contains(out, "Astra") {
		t.Fatalf("missing brand in view:\n%s", out)
	}
}

func TestStatusBarNarrowWideChars(t *testing.T) {
	a := newTestApp(t)
	a.width = 40
	a.status = strings.Repeat("界", 30) // wide glyphs: rune-truncation can exceed column budget
	a.lastUsage = llm.Usage{InputTokens: 1000, OutputTokens: 1000}
	a.totalCost = 0.5
	out := a.renderStatusBar()
	if out == "" {
		t.Fatal("status bar rendered empty")
	}
}

func TestStatusLineSegments(t *testing.T) {
	a := newTestApp(t)
	a.lastUsage = llm.Usage{InputTokens: 1000, OutputTokens: 2000}
	segs := a.statusLineSegments()
	if len(segs) == 0 {
		t.Fatal("status line rendered empty")
	}
	found := false
	for _, s := range segs {
		if strings.Contains(s, a.engine.Model) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("status line missing model: %v", segs)
	}
}

func TestOverlayShortcutsCodexStrings(t *testing.T) {
	a := newTestApp(t)
	o := overlayShortcuts(a.engine)
	if !strings.Contains(o.body, "/  for commands") ||
		!strings.Contains(o.body, "esc esc  to edit previous message") ||
		!strings.Contains(o.body, "customize shortcuts with /keymap") {
		t.Fatalf("shortcuts overlay not Codex-aligned:\n%s", o.body)
	}
}

func TestComposerImageAttachmentRendered(t *testing.T) {
	c := newComposer(80)
	c.AddImage("/tmp/shot.png")
	out := strip(c.View(80))
	if !strings.Contains(out, "image #1") || !strings.Contains(out, "/tmp/shot.png") {
		t.Fatalf("image attachment not rendered:\n%s", out)
	}
}

func TestToggleStatusLineItem(t *testing.T) {
	items := []string{"model"}
	items = toggleStatusLineItem(items, "model")
	if len(items) != 0 {
		t.Fatalf("expected removal, got %v", items)
	}
	items = toggleStatusLineItem(items, "git-branch")
	if len(items) != 1 || items[0] != "git-branch" {
		t.Fatalf("expected addition, got %v", items)
	}
}

func TestOverlayHelpCyclesTabs(t *testing.T) {
	a := newTestApp(t)
	a.overlay = overlayHelp()
	if a.overlay == nil || len(a.overlay.tabs) == 0 {
		t.Fatal("help overlay empty tabs")
	}
	// Press right-arrow twice to move tab index.
	_, _ = a.Update(tea.KeyMsg{Type: tea.KeyRight})
	_, _ = a.Update(tea.KeyMsg{Type: tea.KeyRight})
	if a.overlay.tab < 1 {
		t.Fatalf("tab not advancing: %d", a.overlay.tab)
	}
}

func TestCostEstimate(t *testing.T) {
	u := approximateCost("claude-sonnet-4-20250514", llm.Usage{InputTokens: 1_000_000, OutputTokens: 0})
	if u < 2.9 || u > 3.1 {
		t.Fatalf("cost out of expected window: %f", u)
	}
}

func TestDiffRenderer(t *testing.T) {
	body := "--- a/foo.go\n+++ b/foo.go\n@@ -1,2 +1,2 @@\n-old\n+new\n"
	r := renderDiff(body, "foo.go")
	if !strings.Contains(r, "+new") || !strings.Contains(r, "-old") {
		t.Fatalf("diff missing lines:\n%s", r)
	}
}

func TestSlashFilter(t *testing.T) {
	c := newComposer(80)
	c.ta.SetValue("/mo")
	c.refresh()
	if len(c.filtered) == 0 {
		t.Fatal("expected slash suggestions")
	}
}

func TestBashModeToggle(t *testing.T) {
	c := newComposer(80)
	c.EnterBash()
	if !c.IsBash() {
		t.Fatal("bash mode did not engage")
	}
	c.bashLine = "ls -la"
	c.ExitBash()
	if c.IsBash() {
		t.Fatal("bash mode did not exit")
	}
}

func TestPaletteFuzzy(t *testing.T) {
	p := palette{filtered: builtinPaletteOnce()}
	p.input = "ses"
	p.refilter()
	if len(p.filtered) == 0 {
		t.Fatal("palette did not match 'ses'")
	}
}

func TestThemeSwitch(t *testing.T) {
	if CurrentTheme() != "astra" {
		t.Fatalf("default theme should be astra, got %q", CurrentTheme())
	}
	if SetTheme("nope") != "" {
		t.Fatal("unknown theme name should be rejected")
	}
	defer SetTheme("astra")
	if applied := SetTheme("astra-light"); applied != "astra-light" {
		t.Fatalf("SetTheme = %q", applied)
	}
	if CurrentTheme() != "astra-light" {
		t.Fatalf("theme did not switch, %q", CurrentTheme())
	}
}

func TestModelSettingsForm(t *testing.T) {
	a := newTestApp(t)
	s := newModelSettings(a)
	if s.zone != settingsZoneFields {
		t.Fatalf("single provider should start in the fields zone, got %v", s.zone)
	}
	if got := s.fields[0].Value(); got != "https://example.invalid/v1" {
		t.Fatalf("url field = %q", got)
	}
	if got := s.fields[2].Value(); got != "test-model" {
		t.Fatalf("model field = %q", got)
	}

	// Editing + Save persists through the engine.
	s.fields[0].SetValue("https://new.example/v1")
	done, msg, _ := s.runButton("save", a)
	if !done {
		t.Fatalf("save should close the form, err=%q", s.err)
	}
	if !strings.Contains(msg, "test") {
		t.Fatalf("save message = %q", msg)
	}
	if got := a.engine.Config.Providers[0].BaseURL; got != "https://new.example/v1" {
		t.Fatalf("engine url = %q", got)
	}

	// Save & Use activates the model.
	s3 := newModelSettings(a)
	done, _, _ = s3.runButton("save-use", a)
	if !done || s3.err != "" {
		t.Fatalf("save-use should succeed, err=%q", s3.err)
	}
	if a.engine.Model != "test-model" {
		t.Fatalf("engine model = %q", a.engine.Model)
	}

	// Add provider → cancel rolls the in-memory provider back out.
	s2 := newModelSettings(a)
	orig := len(s2.providers)
	s2.addProvider()
	if len(s2.providers) != orig+1 {
		t.Fatalf("addProvider should append one provider, got %d", len(s2.providers))
	}
	if len(a.engine.Config.Providers) != orig+1 {
		t.Fatalf("engine config should carry the in-memory provider")
	}
	done, _, _ = s2.cancel(a)
	if !done {
		t.Fatal("cancel should close")
	}
	if len(a.engine.Config.Providers) != orig {
		t.Fatalf("cancel should roll back the added provider, got %d", len(a.engine.Config.Providers))
	}
}

func TestModelSettingsKeys(t *testing.T) {
	a := newTestApp(t)
	s := newModelSettings(a)
	// Tab walks URL → key → model → buttons, esc walks back.
	_, _, _ = s.handleKey(tea.KeyMsg{Type: tea.KeyTab}, a)
	if s.zone != settingsZoneFields || s.fieldSel != 1 {
		t.Fatalf("tab should move to the key field, got zone=%v field=%d", s.zone, s.fieldSel)
	}
	_, _, _ = s.handleKey(tea.KeyMsg{Type: tea.KeyTab}, a)
	if s.zone != settingsZoneFields || s.fieldSel != 2 {
		t.Fatalf("tab should move to the model field, got zone=%v field=%d", s.zone, s.fieldSel)
	}
	_, _, _ = s.handleKey(tea.KeyMsg{Type: tea.KeyTab}, a)
	if s.zone != settingsZoneButtons {
		t.Fatalf("tab after the last field should move to buttons, got zone=%v", s.zone)
	}
	_, _, _ = s.handleKey(tea.KeyMsg{Type: tea.KeyEsc}, a)
	if s.zone != settingsZoneFields {
		t.Fatalf("esc should return to the fields zone, got zone=%v", s.zone)
	}
}

func TestReverseSearch(t *testing.T) {
	c := newComposer(80)
	c.history = []string{"hi there", "find foo bar", "another line", "foo baz"}
	c.searchQuery = "foo"
	c.recomputeSearch()
	if len(c.searchHits) != 2 {
		t.Fatalf("expected 2 hits for 'foo', got %d", len(c.searchHits))
	}
	c.searchQuery = "nope"
	c.recomputeSearch()
	if len(c.searchHits) != 0 || c.searchPos != -1 {
		t.Fatalf("expected no hits, got %+v", c.searchHits)
	}
}
