package tui

import "github.com/charmbracelet/lipgloss"

// Astra theme: deep space blues with violet accent.
var (
	accent   = lipgloss.Color("#8B7CF6")
	accentHi = lipgloss.Color("#B4A7FF")
	cyan     = lipgloss.Color("#5FD4FF")
	green    = lipgloss.Color("#4ADE80")
	yellow   = lipgloss.Color("#FACC15")
	red      = lipgloss.Color("#F87171")
	gray     = lipgloss.Color("#6B7280")
	white    = lipgloss.Color("#E5E7EB")

	styleHeader = lipgloss.NewStyle().
			Foreground(accentHi).
			Bold(true)

	styleTitle = lipgloss.NewStyle().
			Foreground(accentHi).
			Bold(true)

	styleDim = lipgloss.NewStyle().Foreground(gray)

	styleUserBox = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(cyan).
			Padding(0, 1).
			MaxWidth(100)

	styleAssistantBox = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(accent).
				Padding(0, 1)

	styleToolBox = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(gray).
			Padding(0, 1).
			MarginTop(1)

	styleSystem = lipgloss.NewStyle().Foreground(gray).Italic(true)

	styleError = lipgloss.NewStyle().Foreground(red).Bold(true)

	styleSuccess = lipgloss.NewStyle().Foreground(green).Bold(true)

	styleWarn = lipgloss.NewStyle().Foreground(yellow)

	styleChip = lipgloss.NewStyle().
			Padding(0, 1).
			MarginTop(1).
			MarginBottom(1)

	styleStatusBar = lipgloss.NewStyle().
			Foreground(white).
			Background(lipgloss.Color("#232136")).
			Padding(0, 1)

	styleComposer = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(accent).
			Padding(0, 1)

	styleOverlay = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(accent).
			Padding(1, 2).
			MaxWidth(110)
)

func chip(kind string) string {
	style := styleChip
	switch kind {
	case "evidence":
		style = style.Background(lipgloss.Color("#123524")).Foreground(green)
	case "unknown":
		style = style.Background(lipgloss.Color("#3A2E0A")).Foreground(yellow)
	case "claim":
		style = style.Background(lipgloss.Color("#1E1B4B")).Foreground(accentHi)
	}
	return style.Render(kind)
}
