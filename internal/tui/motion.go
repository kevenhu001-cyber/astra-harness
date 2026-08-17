package tui

import (
	"os"
	"strings"
)

// motionEnabled reports whether time-based animation (shimmer bands,
// frame spinners, effort ignition) may run. Codex reads the OS
// reduced-motion preference; Astra honors the same intent through
// explicit environment opt-outs, and the shared tick below keeps every
// animated surface in one sync.
func motionEnabled() bool {
	if os.Getenv("ASTRA_REDUCE_MOTION") != "" || os.Getenv("REDUCE_MOTION") != "" {
		return false
	}
	return true
}

// trueColorTerm reports whether the terminal advertises truecolor support,
// which drives the RGB shimmer band. Without it the shimmer falls back to a
// dim → plain → bold ladder, mirroring codex-rs shimmer.rs color_for_level.
func trueColorTerm() bool {
	ct := os.Getenv("COLORTERM")
	if strings.Contains(ct, "truecolor") || strings.Contains(ct, "24bit") {
		return true
	}
	return strings.Contains(os.Getenv("TERM"), "truecolor")
}
