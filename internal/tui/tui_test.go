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

func TestProviderSettingsForm(t *testing.T) {
	a := newTestApp(t)
	s := newProviderSettings(a)
	if s.view != pvPicker {
		t.Fatalf("should start on the picker, got %v", s.view)
	}

	// Open the editor for the single test provider.
	s.openEditor(a, a.engine.Config.Providers[0], false)
	ed := &s.ed
	if got := ed.fields[1].ti.Value(); got != "https://example.invalid/v1" {
		t.Fatalf("base url field = %q", got)
	}
	if ed.defModel != "test-model" {
		t.Fatalf("default model = %q", ed.defModel)
	}

	// Editing + Save persists through the engine.
	ed.fields[1].ti.SetValue("https://new.example/v1")
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
	s2 := newProviderSettings(a)
	s2.openEditor(a, a.engine.Config.Providers[0], false)
	done, _, _ = s2.runButton("save-use", a)
	if !done || s2.err != "" {
		t.Fatalf("save-use should succeed, err=%q", s2.err)
	}
	if a.engine.Model != "test-model" {
		t.Fatalf("engine model = %q", a.engine.Model)
	}

	// Add a custom provider → Save persists it; Delete rolls it back.
	s3 := newProviderSettings(a)
	s3.openCustom(a)
	if !s3.ed.isNew {
		t.Fatal("custom form should be marked isNew")
	}
	s3.ed.fields[0].ti.SetValue("mycustom") // ID
	s3.ed.fields[1].ti.SetValue("My Custom") // Name
	s3.ed.fields[3].ti.SetValue("https://custom.example/v1") // BaseURL
	done, _, _ = s3.runButton("save", a)
	if !done {
		t.Fatalf("save custom should close, err=%q", s3.err)
	}
	if len(a.engine.Config.Providers) != 2 {
		t.Fatalf("should have 2 providers, got %d", len(a.engine.Config.Providers))
	}
	s4 := newProviderSettings(a)
	s4.openEditor(a, a.engine.Config.Providers[1], false)
	_, _, _ = s4.runButton("delete", a)
	if s4.view != pvPicker {
		t.Fatalf("delete should return to the picker, view=%v", s4.view)
	}
	if len(a.engine.Config.Providers) != 1 {
		t.Fatalf("delete should remove the provider, got %d", len(a.engine.Config.Providers))
	}
}

func TestProviderSettingsKeys(t *testing.T) {
	a := newTestApp(t)
	s := newProviderSettings(a)

	// Picker navigation.
	_, _, _ = s.handleKey(tea.KeyMsg{Type: tea.KeyDown}, a)
	if s.pickSel != 1 {
		t.Fatalf("down should move to the custom card, pickSel=%d", s.pickSel)
	}
	_, _, _ = s.handleKey(tea.KeyMsg{Type: tea.KeyUp}, a)
	if s.pickSel != 0 {
		t.Fatalf("up should move back, pickSel=%d", s.pickSel)
	}

	// Editor: Tab walks Name → BaseURL → APIKey → Models, esc returns to picker.
	s.openEditor(a, a.engine.Config.Providers[0], false)
	_, _, _ = s.handleKey(tea.KeyMsg{Type: tea.KeyTab}, a)
	if s.ed.zone != zoneFields || s.ed.fieldIdx != 1 {
		t.Fatalf("tab should move to base url, got zone=%v field=%d", s.ed.zone, s.ed.fieldIdx)
	}
	_, _, _ = s.handleKey(tea.KeyMsg{Type: tea.KeyTab}, a)
	if s.ed.zone != zoneFields || s.ed.fieldIdx != 2 {
		t.Fatalf("tab should move to api key, got zone=%v field=%d", s.ed.zone, s.ed.fieldIdx)
	}
	_, _, _ = s.handleKey(tea.KeyMsg{Type: tea.KeyTab}, a)
	if s.ed.zone != zoneModels {
		t.Fatalf("tab after last field should enter models, got zone=%v", s.ed.zone)
	}
	_, _, _ = s.handleKey(tea.KeyMsg{Type: tea.KeyEsc}, a)
	if s.view != pvPicker {
		t.Fatalf("esc should return to the picker, got view=%v", s.view)
	}

	// Custom form: Type field cycles with left/right.
	s.openCustom(a)
	s.ed.fieldIdx = 2 // Type field (ID, Name, Type, BaseURL, APIKey)
	before := s.ed.prov.Type
	_, _, _ = s.handleKey(tea.KeyMsg{Type: tea.KeyRight}, a)
	if s.ed.prov.Type == before {
		t.Fatalf("type should cycle on left/right, got %q", s.ed.prov.Type)
	}
}

func TestProviderSettingsView(t *testing.T) {
	a := newTestApp(t)
	s := newProviderSettings(a)
	// Picker view must render without panicking.
	if out := s.View(120, 36); out == "" {
		t.Fatal("picker view rendered empty")
	}
	// Editor view (existing provider) must render.
	s.openEditor(a, a.engine.Config.Providers[0], false)
	if out := s.View(120, 36); out == "" {
		t.Fatal("editor view rendered empty")
	}
	// Custom form must render, including the type field and model rows.
	s.openCustom(a)
	s.ed.fields[0].ti.SetValue("acme")
	if out := s.View(120, 36); out == "" {
		t.Fatal("custom form view rendered empty")
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
