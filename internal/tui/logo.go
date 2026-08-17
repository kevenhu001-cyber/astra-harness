package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// pixelLetters is a 5x5 pixel font for the letters in "ASTRA". '#' is a lit
// pixel, '.' is empty. Rendering maps '#' to a double-width block so each
// letter reads as chunky pixel art.
var pixelLetters = map[rune][]string{
	'A': {
		".###.",
		"#...#",
		"#####",
		"#...#",
		"#...#",
	},
	'S': {
		".####",
		"#....",
		".###.",
		"....#",
		"####.",
	},
	'T': {
		"#####",
		"..#..",
		"..#..",
		"..#..",
		"..#..",
	},
	'R': {
		"####.",
		"#...#",
		"####.",
		"#.#..",
		"#...#",
	},
}

// astraLogoLines returns the 5 rows of the pixel-art "ASTRA" wordmark,
// each already styled with the given style. The glyphs are 5x5 and each
// letter is 8 columns wide (5 double-width pixels + 3-col gap), so the
// whole wordmark is 40 columns plus a right margin.
func astraLogoLines(style lipgloss.Style) []string {
	rows := make([]string, 5)
	for i := 0; i < 5; i++ {
		var b strings.Builder
		for _, r := range "ASTRA" {
			letter, ok := pixelLetters[r]
			if !ok {
				continue
			}
			for _, px := range letter[i] {
				if px == '#' {
					b.WriteString("██")
				} else {
					b.WriteString("  ")
				}
			}
			b.WriteString("  ")
		}
		rows[i] = style.Render(strings.TrimRight(b.String(), " "))
	}
	return rows
}

// renderPixelLogo renders the ASTRA wordmark centered on the given width.
func renderPixelLogo(width int) string {
	pal := activePalette()
	style := lipgloss.NewStyle().Foreground(pal.Accent).Bold(true)
	lines := astraLogoLines(style)
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		out = append(out, lipgloss.NewStyle().Width(width).Align(lipgloss.Center).Render(l))
	}
	return strings.Join(out, "\n")
}
