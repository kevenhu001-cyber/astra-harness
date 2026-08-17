package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/kevenhu001-cyber/astra-harness/internal/engine"
)

func TestMotionEnvGating(t *testing.T) {
	t.Setenv("ASTRA_REDUCE_MOTION", "")
	t.Setenv("REDUCE_MOTION", "")
	if !motionEnabled() {
		t.Fatal("motion should be enabled by default")
	}
	t.Setenv("REDUCE_MOTION", "1")
	if motionEnabled() {
		t.Fatal("REDUCE_MOTION should disable motion")
	}
	t.Setenv("REDUCE_MOTION", "")
	t.Setenv("ASTRA_REDUCE_MOTION", "1")
	if motionEnabled() {
		t.Fatal("ASTRA_REDUCE_MOTION should disable motion")
	}
}

func TestAsciiAnimTickAndView(t *testing.T) {
	anim := newAsciiAnim(framesDots)
	first := anim.view()
	if first == "" {
		t.Fatal("anim rendered empty")
	}
	anim.tick()
	second := anim.view()
	if second == first {
		t.Fatal("tick did not advance the frame")
	}
	// Circular: after len(frames) ticks the frame repeats.
	anim = newAsciiAnim(framesShapes)
	baseline := anim.view()
	for i := 0; i < len(framesShapes); i++ {
		anim.tick()
	}
	if anim.view() != baseline {
		t.Fatal("frame did not wrap around")
	}
}

func TestActivityBulletReducedMotion(t *testing.T) {
	t.Setenv("ASTRA_REDUCE_MOTION", "1")
	got := strip(activityBullet(time.Now()))
	if got != "•" {
		t.Fatalf("reduced-motion bullet = %q", got)
	}
}

func TestShimmerRenderStaticUnderReducedMotion(t *testing.T) {
	t.Setenv("ASTRA_REDUCE_MOTION", "1")
	c := newComposer(80)
	out := strip(c.placeholderView())
	if !strings.Contains(out, "› Ask Astra to do anything") {
		t.Fatalf("placeholder mismatch: %q", out)
	}
}

func TestShimmerLevelSweeps(t *testing.T) {
	// n=30 gives period 50; at x=400ms the sweep position is exactly 10,
	// putting the band center on rune 0 (i + padding).
	base := time.Unix(0, 0)
	center := time.Unix(0, 400_000_000)
	if got := shimmerLevel(0, 30, center); got != 1.0 {
		t.Fatalf("band center should be 1.0, got %v", got)
	}
	if got := shimmerLevel(0, 30, base); got != 0 {
		t.Fatalf("rune far from the band should be 0, got %v", got)
	}
	// One second later the band has moved to the far side: the first rune
	// must not be at the same intensity as at t=0.
	at := shimmerLevel(0, 30, base.Add(time.Second))
	if at != 0 {
		t.Fatalf("band should have left rune 0 after 1s, got %v", at)
	}
	// All intensities stay in [0,1].
	for i := 0; i < 30; i++ {
		if v := shimmerLevel(i, 30, center); v < 0 || v > 1 {
			t.Fatalf("intensity out of range at %d: %v", i, v)
		}
	}
}

func TestShimmerTextPreserved(t *testing.T) {
	t.Setenv("COLORTERM", "truecolor")
	got := strip(shimmerRender("hello", time.Unix(0, 0)))
	if got != "hello" {
		t.Fatalf("shimmer altered the text: %q", got)
	}
}

func TestContainsPlanKeyword(t *testing.T) {
	// Mirrors codex-rs chatwidget/tests/plan_mode.rs cases.
	cases := []struct {
		text string
		want bool
	}{
		{"plan", true},
		{"Make a Plan first.", true},
		{"plane", false},
		{"planning", false},
		{"/plan", true},
		{"!plan", true},
		{"implement the migration", false},
		{"plan_step_1", false}, // "_" binds the token, so it is not the word "plan"
	}
	for _, c := range cases {
		if got := containsPlanKeyword(c.text); got != c.want {
			t.Fatalf("containsPlanKeyword(%q) = %v, want %v", c.text, got, c.want)
		}
	}
}

func TestPlanNudgePolicy(t *testing.T) {
	a := newTestApp(t)
	a.composer.SetValue("write a plan for the refactor")
	if !a.nudgeVisible() {
		t.Fatal("nudge should show for a draft mentioning plan")
	}
	a.planNudgeDismissed = true
	if a.nudgeVisible() {
		t.Fatal("dismissed nudge should stay hidden")
	}
	a.planNudgeDismissed = false
	a.engine.Perm.SetPlanMode(true)
	if a.nudgeVisible() {
		t.Fatal("nudge must not show while plan mode is active")
	}
	a.engine.Perm.SetPlanMode(false)
	a.composer.SetValue("/plan")
	if a.nudgeVisible() {
		t.Fatal("nudge must not show for slash drafts")
	}
	a.composer.SetValue("plane geometry")
	if a.nudgeVisible() {
		t.Fatal("'plane' is not the keyword 'plan'")
	}
}

func TestPlanNudgeRender(t *testing.T) {
	a := newTestApp(t)
	a.composer.SetValue("make a plan")
	got := strip(a.renderNudge())
	if !strings.Contains(got, "Create a plan?") ||
		!strings.Contains(got, "shift + tab use Plan mode") ||
		!strings.Contains(got, "esc dismiss") {
		t.Fatalf("nudge line mismatch: %q", got)
	}
	a.composer.SetValue("hello")
	if a.renderNudge() != "" {
		t.Fatal("nudge rendered without keyword")
	}
}

func TestCyclePermissionMode(t *testing.T) {
	a := newTestApp(t)
	a.engine.Perm.SetMode(engine.ModeAsk)
	a.engine.Perm.SetPlanMode(false)
	a.cyclePermissionMode() // ask -> plan
	if !a.engine.Perm.IsPlanMode() {
		t.Fatal("cycle did not enter plan mode")
	}
	a.cyclePermissionMode() // plan -> allow
	if a.engine.Perm.IsPlanMode() || a.engine.Perm.GetMode() != engine.ModeAllow {
		t.Fatalf("cycle to allow failed: mode=%s plan=%v", a.engine.Perm.GetMode(), a.engine.Perm.IsPlanMode())
	}
	a.cyclePermissionMode() // allow -> deny
	if a.engine.Perm.GetMode() != engine.ModeDeny {
		t.Fatalf("cycle to deny failed: %s", a.engine.Perm.GetMode())
	}
	a.cyclePermissionMode() // deny -> ask
	if a.engine.Perm.GetMode() != engine.ModeAsk {
		t.Fatalf("cycle to ask failed: %s", a.engine.Perm.GetMode())
	}
}

func TestSyncTitleActionRequired(t *testing.T) {
	a := newTestApp(t)
	// Can't observe the SetWindowTitle command payload directly; just ensure
	// the helper returns a command in both states without panicking.
	if a.syncTitle() == nil {
		t.Fatal("syncTitle returned no command")
	}
	a.mode = modePermission
	if a.syncTitle() == nil {
		t.Fatal("syncTitle returned no command while permission pending")
	}
	a.mode = modeAsk
	if a.syncTitle() == nil {
		t.Fatal("syncTitle returned no command while ask pending")
	}
}

func TestRenderAssistantStreaming(t *testing.T) {
	t.Setenv("ASTRA_REDUCE_MOTION", "1")
	a := newTestApp(t)
	a.streaming = &streamingMsg{start: time.Now(), raw: "first line\n\nsecond line"}
	a.streaming.committed = "first line\n\n"
	a.streaming.tail = "second line"
	got := strip(a.renderAssistantStreaming())
	lines := strings.Split(got, "\n")
	if !strings.HasPrefix(lines[0], "• first line") {
		t.Fatalf("streaming first line missing bullet: %q", lines[0])
	}
	if lines[1] != "  " || !strings.HasPrefix(lines[2], "  second line") {
		t.Fatalf("streaming continuation gutter mismatch: %q", lines[1:])
	}
	// Empty fallback: empty committed + empty tail renders the ellipsis placeholder.
	a.streaming = &streamingMsg{start: time.Now()}
	empty := strip(a.renderAssistantStreaming())
	if !strings.Contains(empty, "•") || !strings.Contains(empty, "…") {
		t.Fatalf("empty stream placeholder mismatch: %q", empty)
	}
}

func TestStreamingCommit(t *testing.T) {
	a := newTestApp(t)
	// Simulate the EvDelta accumulator: deltas land in tail; a newline moves
	// everything up to and including that newline into committed.
	a.streaming = &streamingMsg{start: time.Now()}
	apply := func(text string) {
		a.streaming.tail += text
		if i := strings.LastIndexByte(a.streaming.tail, '\n'); i >= 0 {
			a.streaming.committed += a.streaming.tail[:i+1]
			a.streaming.tail = a.streaming.tail[i+1:]
			a.streaming.cached = ""
		}
	}
	apply("Hello")
	if a.streaming.committed != "" || a.streaming.tail != "Hello" {
		t.Fatalf("partial line should stay in tail, got %q/%q", a.streaming.committed, a.streaming.tail)
	}
	apply(", world\nNow with a list:\n- one\n- two")
	if a.streaming.committed != "Hello, world\nNow with a list:\n- one\n" {
		t.Fatalf("unexpected committed: %q", a.streaming.committed)
	}
	if a.streaming.tail != "- two" {
		t.Fatalf("unexpected tail: %q", a.streaming.tail)
	}
}

func TestFmtElapsedCompact(t *testing.T) {
	cases := map[time.Duration]string{
		0:                "0s",
		5 * time.Second:  "5s",
		59 * time.Second: "59s",
		time.Minute:      "1m 00s",
		90 * time.Second: "1m 30s",
		time.Hour:        "1h 00m 00s",
		3*time.Hour + 4*time.Minute + 5*time.Second: "3h 04m 05s",
	}
	for d, want := range cases {
		if got := fmtElapsedCompact(d); got != want {
			t.Fatalf("fmtElapsedCompact(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestRenderStatusIndicatorBusy(t *testing.T) {
	t.Setenv("ASTRA_REDUCE_MOTION", "1")
	a := newTestApp(t)
	if got := a.renderStatusIndicator(); got != "" {
		t.Fatalf("indicator should be empty when idle: %q", got)
	}
	a.busy = true
	a.busyAt = time.Now().Add(-90 * time.Second)
	got := strip(a.renderStatusIndicator())
	if !strings.Contains(got, "Working") || !strings.Contains(got, "1m 30s") {
		t.Fatalf("indicator should include label + elapsed: %q", got)
	}
	if !strings.Contains(got, "to interrupt") {
		t.Fatalf("indicator should advertise the interrupt shortcut: %q", got)
	}
}

func TestStripOrdinal(t *testing.T) {
	cases := map[string]string{
		"1. Sessions tab":   "Sessions tab",
		"12. edge":          "edge",
		"  1. Sessions tab": "Sessions tab",
		"› 1. Sessions tab": "Sessions tab",
		"abc":               "abc",
		"  1.1 nested":      "1.1 nested", // "1.1" is not "N." followed by space
		"  0. zero":         "zero",
	}
	for in, want := range cases {
		if got := stripOrdinal(in); got != want {
			t.Fatalf("stripOrdinal(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFormatDirectoryDisplay(t *testing.T) {
	home, _ := os.UserHomeDir()
	if home != "" {
		if got := formatDirectoryDisplay(home); got != "~" {
			t.Fatalf("home dir should render as ~: %q", got)
		}
		if got := formatDirectoryDisplay(filepath.Join(home, "proj")); got != "~"+string(os.PathSeparator)+"proj" {
			t.Fatalf("subdir should render as ~/proj: %q", got)
		}
	}
	if got := formatDirectoryDisplay("/tmp/not-in-home"); got != "/tmp/not-in-home" {
		t.Fatalf("unrelated path should stay unchanged: %q", got)
	}
}

func TestSidebarTabsRender(t *testing.T) {
	a := newTestApp(t)
	_, _ = a.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	a.sidebar.visible = true
	a.layout()
	out := strip(a.sidebar.View())
	for _, label := range a.sidebar.tabs {
		if !strings.Contains(out, label) {
			t.Fatalf("sidebar tabs missing %q:\n%s", label, out)
		}
	}
	if !strings.Contains(out, "↑↓ navigate") {
		t.Fatalf("sidebar hint footer missing:\n%s", out)
	}
}

func TestSidebarTabSwitch(t *testing.T) {
	a := newTestApp(t)
	a.sidebar.visible = true
	a.sidebar.mode = sidebarSessions
	a.sidebar.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	if a.sidebar.mode != sidebarGoals {
		t.Fatalf("expected sidebar mode to switch to Knowledge via '3', got %v", a.sidebar.mode)
	}
}

func TestComposerHintVariants(t *testing.T) {
	a := newTestApp(t)
	if !strings.Contains(a.renderComposerHint(), "? for shortcuts") {
		t.Fatalf("idle composer should advertise ? for shortcuts: %q", a.renderComposerHint())
	}
	a.busy = true
	if !strings.Contains(a.renderComposerHint(), "queue message") {
		t.Fatalf("busy composer should advertise queue message: %q", a.renderComposerHint())
	}
	a.busy = false
	a.mode = modePermission
	if !strings.Contains(a.renderComposerHint(), "esc to cancel") {
		t.Fatalf("permission composer should advertise esc to cancel: %q", a.renderComposerHint())
	}
}

func TestStatusBarPlanPill(t *testing.T) {
	a := newTestApp(t)
	a.engine.SetStatusLine([]string{"permissions"})
	a.engine.Perm.SetPlanMode(true)
	defer a.engine.Perm.SetPlanMode(false)
	segs := a.statusLineSegments()
	found := false
	for _, s := range segs {
		if strings.Contains(stripANSI(s), "plan") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("plan-mode status segment missing: %v", segs)
	}
}
