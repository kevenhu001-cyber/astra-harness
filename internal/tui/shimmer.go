package tui

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// shimmerRender mirrors codex-rs shimmer_spans (shimmer.rs): a cosine band
// sweeps across the text every 2 seconds, brightening runes from the base
// color toward the highlight color (truecolor) or through a dim → plain →
// bold ladder (ANSI16/256). The sweep phase is derived from the wall clock so
// every shimmering surface in the viewport stays in sync.
func shimmerRender(text string, now time.Time) string {
	runes := []rune(text)
	if len(runes) == 0 {
		return ""
	}
	pal := activePalette()
	tc := trueColorTerm()
	var b strings.Builder
	for i, r := range runes {
		t := shimmerLevel(i, len(runes), now)
		if tc {
			st := lipgloss.NewStyle().Foreground(blendHex(pal.WhiteDim, pal.AccentHi, t))
			if t > 0.35 {
				st = st.Bold(true)
			}
			b.WriteString(st.Render(string(r)))
		} else {
			switch {
			case t < 0.2:
				b.WriteString(styleFaint.Render(string(r)))
			case t < 0.6:
				b.WriteString(string(r))
			default:
				b.WriteString(styleEmph.Render(string(r)))
			}
		}
	}
	return b.String()
}

// shimmerLevel returns the band intensity in [0,1] for rune i of an n-rune
// text at time now, mirroring codex-rs's cosine band sweep. Pure so tests
// can assert the sweep math without a color-capable terminal.
func shimmerLevel(i, n int, now time.Time) float64 {
	const (
		padding      = 10
		sweepSeconds = 2.0
		bandHalf     = 5.0
	)
	period := n + padding*2
	posF := float64(now.UnixNano()%int64(sweepSeconds*1e9)) / (sweepSeconds * 1e9) * float64(period)
	pos := int(posF)
	dist := math.Abs(float64(i + padding - pos))
	if dist > bandHalf {
		return 0
	}
	return 0.5 * (1 + math.Cos(math.Pi*dist/bandHalf))
}

// blendHex linearly interpolates two hex colors. Falls back to `to` when
// either color is not a hex literal (e.g. named ANSI colors).
func blendHex(from, to lipgloss.Color, t float64) lipgloss.Color {
	fr, fg, fb, ok1 := parseHex(string(from))
	tr, tg, tb, ok2 := parseHex(string(to))
	if !ok1 || !ok2 {
		return to
	}
	t = math.Max(0, math.Min(1, t))
	return lipgloss.Color(fmt.Sprintf("#%02x%02x%02x",
		uint8(float64(fr)+float64(tr-fr)*t),
		uint8(float64(fg)+float64(tg-fg)*t),
		uint8(float64(fb)+float64(tb-fb)*t)))
}

func parseHex(s string) (r, g, b int, ok bool) {
	s = strings.TrimPrefix(s, "#")
	if len(s) != 6 {
		return 0, 0, 0, false
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return 0, 0, 0, false
	}
	return int(v >> 16 & 0xff), int(v >> 8 & 0xff), int(v & 0xff), true
}

// activityBullet returns the animated bullet used by live exec cells:
// a shimmering "•" on truecolor, a 600ms "•"/"◦" blink otherwise, and a
// static dim bullet under reduced motion (codex-rs motion.rs
// animated_activity_indicator).
func activityBullet(now time.Time) string {
	if !motionEnabled() {
		return styleBullet.Render(codexBullet)
	}
	if trueColorTerm() {
		return shimmerRender(codexBullet, now)
	}
	if (now.UnixMilli()/600)%2 == 0 {
		return styleBullet.Render(codexBullet)
	}
	return styleDim.Render("◦")
}
