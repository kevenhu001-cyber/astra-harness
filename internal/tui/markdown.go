package tui

import (
	"github.com/charmbracelet/glamour"
)

var mdRenderer *glamour.TermRenderer

func init() {
	mdRenderer, _ = glamour.NewRenderer(
		glamour.WithWordWrap(96),
		glamour.WithStandardStyle("dark"),
	)
}

// renderMarkdown converts markdown to ANSI text for the viewport.
func renderMarkdown(src string) string {
	if src == "" {
		return ""
	}
	out, err := mdRenderer.Render(src)
	if err != nil {
		return src
	}
	return out
}

func setMarkdownWidth(w int) {
	if w < 30 {
		w = 30
	}
	mdRenderer, _ = glamour.NewRenderer(
		glamour.WithWordWrap(w-8),
		glamour.WithStandardStyle("dark"),
	)
}
