package tui

import "github.com/charmbracelet/lipgloss"

// Built-in ASCII animation frame sets, mirroring codex-rs tui/src/frames.rs
// (which ships ten 36-frame variants). These are compact single-line spinners
// so they can sit in the header, status line and startup banner, and they
// share the app's animation tick with the shimmer band.
var (
	framesDots    = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	framesBlocks  = []string{"▁", "▂", "▃", "▄", "▅", "▆", "▇", "█", "▇", "▆", "▅", "▄", "▃", "▂"}
	framesHash    = []string{"▏", "▎", "▍", "▌", "▋", "▊", "▉", "▊", "▋", "▌", "▍", "▎"}
	framesShapes  = []string{"◐", "◓", "◑", "◒"}
	framesPulse   = []string{"·", "•", "●", "○"}
	allFrameSets  = [][]string{framesDots, framesBlocks, framesHash, framesShapes, framesPulse}
)

// asciiAnim is a tiny frame player driven by the app's animation tick. It
// replaces the bubbles spinner so the header, startup screen and status line
// share one animation cadence with the shimmer band.
type asciiAnim struct {
	frames []string
	idx    int
	style  lipgloss.Style
}

func newAsciiAnim(frames []string) *asciiAnim {
	return &asciiAnim{frames: frames, style: lipgloss.NewStyle().Foreground(activePalette().AccentHi)}
}

func (s *asciiAnim) tick() {
	if len(s.frames) == 0 {
		return
	}
	s.idx = (s.idx + 1) % len(s.frames)
}

func (s *asciiAnim) view() string {
	if len(s.frames) == 0 {
		return ""
	}
	return s.style.Render(s.frames[s.idx%len(s.frames)])
}
