package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// effortIgnition mirrors codex-rs bottom_pane/effort_ignition.rs: a one-shot
// celebration shown in the composer band when the reasoning effort switches
// to Max/Ultra. Phases: slide-in (charge, ~500ms), hold (~1300ms), fade
// (~800ms). Skipped entirely under reduced motion (callers gate creation).
type effortIgnition struct {
	label string // "MAX" | "ULTRA"
	start time.Time
	hues  [3]rgb
}

type rgb struct{ r, g, b uint8 }

const (
	ignitionTotal   = 2600 * time.Millisecond
	ignitionSlideIn = 500 * time.Millisecond
	ignitionHold    = 1800 * time.Millisecond
)

// newEffortIgnition builds the animation for a tier label.
func newEffortIgnition(label string) *effortIgnition {
	ig := &effortIgnition{label: label, start: time.Now()}
	if label == "ULTRA" {
		ig.hues = [3]rgb{{186, 130, 255}, {255, 120, 220}, {120, 170, 255}}
	} else {
		ig.hues = [3]rgb{{255, 178, 66}, {255, 214, 120}, {255, 120, 60}}
	}
	return ig
}

func (ig *effortIgnition) done(now time.Time) bool {
	return now.Sub(ig.start) >= ignitionTotal
}

// view renders the ignition in the composer band: the tier glyphs slide in
// from the right, hold, and fade toward the terminal background, with a
// charge bar beneath that fills and drains with the alpha envelope.
func (ig *effortIgnition) view(width int, now time.Time) string {
	elapsed := now.Sub(ig.start)
	alpha := 1.0
	switch {
	case elapsed < ignitionSlideIn:
		alpha = float64(elapsed) / float64(ignitionSlideIn)
	case elapsed < ignitionHold:
		alpha = 1
	default:
		alpha = 1 - float64(elapsed-ignitionHold)/float64(ignitionTotal-ignitionHold)
	}
	if alpha <= 0 {
		return ""
	}
	pal := activePalette()
	runes := []rune(ig.label)
	glyphs := make([]string, len(runes))
	for i, r := range runes {
		h := ig.hues[i%3]
		col := lipgloss.Color(fmt.Sprintf("#%02x%02x%02x", h.r, h.g, h.b))
		col = blendHex(col, pal.Bg0, 1-alpha)
		glyphs[i] = lipgloss.NewStyle().Foreground(col).Bold(true).Render(string(r))
	}
	label := strings.Join(glyphs, "")
	// Slide from 30% right-of-center into place during the charge.
	slide := 0.30 * (1 - alpha)
	labelW := lipgloss.Width(label)
	left := (width-labelW)/2 + int(slide*float64(width)/2)
	if left < 0 {
		left = 0
	}
	if left+labelW > width {
		left = max(0, width-labelW)
	}
	barLen := int(alpha * float64(labelW))
	if barLen > labelW {
		barLen = labelW
	}
	bar := strings.Repeat("─", barLen)
	barCol := blendHex(ig.hues[0].toColor(), pal.Bg0, 1-alpha)
	line := strings.Repeat(" ", left) + label
	barLine := strings.Repeat(" ", left) + lipgloss.NewStyle().Foreground(barCol).Render(bar)
	return line + "\n" + barLine
}

func (h rgb) toColor() lipgloss.Color {
	return lipgloss.Color(fmt.Sprintf("#%02x%02x%02x", h.r, h.g, h.b))
}
