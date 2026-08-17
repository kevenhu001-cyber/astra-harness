package tui

import (
	"os"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// TestMain pins reduced motion so animated surfaces (live bullets, shimmer
// placeholders) render statically and assertions stay deterministic.
func TestMain(m *testing.M) {
	os.Setenv("ASTRA_REDUCE_MOTION", "1")
	os.Exit(m.Run())
}

// strip renders a Codex-style cell and removes ANSI codes for text assertions.
func strip(s string) string {
	return stripANSI(s)
}

func TestCodexOutputBlockPrefixes(t *testing.T) {
	got := strip(codexOutputBlock("file1\nfile2", toolMaxLines, 80))
	want := "  └ file1\n    file2"
	if got != want {
		t.Fatalf("output block mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestCodexOutputBlockNoOutput(t *testing.T) {
	got := strip(codexOutputBlock("", toolMaxLines, 80))
	if !strings.Contains(got, "└ (no output)") {
		t.Fatalf("missing (no output) placeholder: %q", got)
	}
}

func TestCodexOutputBlockTruncatesWithHint(t *testing.T) {
	lines := make([]string, 0, 10)
	for i := 1; i <= 10; i++ {
		lines = append(lines, "line "+itoa(i))
	}
	got := strip(codexOutputBlock(strings.Join(lines, "\n"), 5, 80))
	if !strings.Contains(got, "line 1") || !strings.Contains(got, "line 2") {
		t.Fatalf("missing head lines:\n%s", got)
	}
	if !strings.Contains(got, "line 9") || !strings.Contains(got, "line 10") {
		t.Fatalf("missing tail lines:\n%s", got)
	}
	if !strings.Contains(got, "… +6 lines (ctrl + t to view transcript)") {
		t.Fatalf("missing transcript hint:\n%s", got)
	}
}

func TestCodexExecCellHeaderAndGutter(t *testing.T) {
	got := strip(codexExecCell(80, false, true, "run_command",
		`{"command":"go test ./..."}`, "ok  github.com/x 3.4s", toolMaxLines))
	if !strings.Contains(got, "• Ran go test ./...") {
		t.Fatalf("missing Ran header:\n%s", got)
	}
	if !strings.Contains(got, "└ ok  github.com/x 3.4s") {
		t.Fatalf("missing output gutter:\n%s", got)
	}
}

func TestCodexExecCellLive(t *testing.T) {
	got := strip(codexExecCell(80, true, false, "run_command",
		`{"command":"cargo build"}`, "Compiling foo...", toolMaxLines))
	if !strings.Contains(got, "• Running cargo build") {
		t.Fatalf("missing Running header:\n%s", got)
	}
	if !strings.Contains(got, "└ Compiling foo...") {
		t.Fatalf("missing live output:\n%s", got)
	}
}

func TestCodexUserShellCell(t *testing.T) {
	got := strip(codexUserShellCell("ls", "file1\nfile2", true, 80))
	if !strings.Contains(got, "• You ran ls") {
		t.Fatalf("missing You ran header:\n%s", got)
	}
	if !strings.Contains(got, "└ file1\n    file2") {
		t.Fatalf("missing shell output block:\n%s", got)
	}
	got = strip(codexUserShellCell("false", "boom", false, 80))
	if !strings.Contains(got, "• You ran false") {
		t.Fatalf("failure cell missing header:\n%s", got)
	}
	if !strings.Contains(got, "boom") {
		t.Fatalf("failure cell missing output:\n%s", got)
	}
}

func TestCodexTranscriptCell(t *testing.T) {
	got := strip(codexTranscriptCell("go test ./...", "ok", true, "", 3200*time.Millisecond))
	if !strings.Contains(got, "$ go test ./...") || !strings.Contains(got, "✓ • 3.2s") {
		t.Fatalf("transcript cell mismatch:\n%s", got)
	}
	got = strip(codexTranscriptCell("ls", "nope", false, "2", 50*time.Millisecond))
	if !strings.Contains(got, "✗ (2) • 50ms") {
		t.Fatalf("failure status mismatch:\n%s", got)
	}
}

func TestCodexSeparator(t *testing.T) {
	plain := strip(codexSeparator("", 20))
	if plain != strings.Repeat("─", 20) {
		t.Fatalf("plain separator mismatch: %q", plain)
	}
	labeled := strip(codexSeparator("Worked for 42s", 30))
	if !strings.Contains(labeled, "─ Worked for 42s ─") || len([]rune(labeled)) != 30 {
		t.Fatalf("labeled separator mismatch: %q (runes=%d)", labeled, len([]rune(labeled)))
	}
}

func TestWrapWords(t *testing.T) {
	lines := wrapWords("one two three four", 9)
	if len(lines) != 3 || lines[0] != "one two" || lines[1] != "three" || lines[2] != "four" {
		t.Fatalf("word wrap mismatch: %q", lines)
	}
	lines = wrapWords("abcdefgh", 4)
	if len(lines) != 2 || lines[0] != "abcd" || lines[1] != "efgh" {
		t.Fatalf("token break mismatch: %q", lines)
	}
	lines = wrapWords("ok  github.com/x 3.4s", 80)
	if len(lines) != 1 || lines[0] != "ok  github.com/x 3.4s" {
		t.Fatalf("spacing must be preserved: %q", lines)
	}
}

func TestRenderUserCodexStyle(t *testing.T) {
	a := newTestApp(t)
	_, _ = a.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	got := strip(a.renderUser("hello\nworld"))
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("expected blank/body/body/blank, got %d lines:\n%q", len(lines), got)
	}
	if !strings.Contains(lines[1], "› hello") || !strings.Contains(lines[2], "  world") {
		t.Fatalf("user prefix/continuation mismatch:\n%q", got)
	}
}

func TestRenderAssistantCodexPrefix(t *testing.T) {
	a := newTestApp(t)
	_, _ = a.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	got := strip(a.renderAssistant("first line\n\nsecond line", 0))
	lines := strings.Split(got, "\n")
	if !strings.HasPrefix(lines[0], "• first line") {
		t.Fatalf("assistant first line missing bullet: %q", lines[0])
	}
	if lines[1] != "  " || !strings.HasPrefix(lines[2], "  second line") {
		t.Fatalf("assistant continuation gutter mismatch: %q", lines[1:])
	}
}

func TestRenderToolCommittedCell(t *testing.T) {
	a := newTestApp(t)
	_, _ = a.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	got := strip(a.renderTool("run_command", true, "ok  github.com/x 3.4s"))
	if !strings.Contains(got, "• Ran") || !strings.Contains(got, "└ ok") {
		t.Fatalf("committed tool cell mismatch:\n%s", got)
	}
}

func TestRenderToolStreamingCell(t *testing.T) {
	a := newTestApp(t)
	_, _ = a.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	a.items = append(a.items, &chatItem{kind: "tool", meta: "run_command", args: `{"command":"cargo build"}`, status: "running"})
	got := strip(a.renderToolStreaming("run_command", "Compiling foo..."))
	if !strings.Contains(got, "• Running cargo build") || !strings.Contains(got, "└ Compiling foo...") {
		t.Fatalf("streaming tool cell mismatch:\n%s", got)
	}
}
