package tui

import "github.com/charmbracelet/lipgloss"

// Astra theme: deep space + violet, with optional light themes.
var (
	// Core palette
	bg0       = lipgloss.Color("#0F0F17")
	bg1       = lipgloss.Color("#15151F")
	bg2       = lipgloss.Color("#1C1C29")
	bg3       = lipgloss.Color("#262638")
	accent    = lipgloss.Color("#8B7CF6") // Astra violet
	accentHi  = lipgloss.Color("#B4A7FF")
	accentLo  = lipgloss.Color("#5B4DC9")
	cyan      = lipgloss.Color("#5FD4FF")
	cyanHi    = lipgloss.Color("#9CE5FF")
	magenta   = lipgloss.Color("#E879F9")
	green     = lipgloss.Color("#4ADE80")
	yellow    = lipgloss.Color("#FACC15")
	orange    = lipgloss.Color("#FB923C")
	red       = lipgloss.Color("#F87171")
	redHi     = lipgloss.Color("#FCA5A5")
	gray      = lipgloss.Color("#6B7280")
	grayLo    = lipgloss.Color("#3F3F55")
	grayHi    = lipgloss.Color("#9CA3AF")
	white     = lipgloss.Color("#E5E7EB")
	whiteDim  = lipgloss.Color("#A0A0B5")

	// Diff palette
	diffAddBg    = lipgloss.Color("#0F2E1F")
	diffAddFg    = lipgloss.Color("#7FE3A6")
	diffAddHiBg  = lipgloss.Color("#14421F")
	diffDelBg    = lipgloss.Color("#3B1313")
	diffDelFg    = lipgloss.Color("#FCA5A5")
	diffDelHiBg  = lipgloss.Color("#5C1B1B")
	diffCtxFg    = lipgloss.Color("#7A7A91")
	diffHunkFg   = lipgloss.Color("#5FD4FF")

	// Styles ---------------------------------------------------------------

	styleTitle  = lipgloss.NewStyle().Foreground(accentHi).Bold(true)
	styleSubtle = lipgloss.NewStyle().Foreground(grayHi)
	styleDim    = lipgloss.NewStyle().Foreground(gray)
	styleFaint  = lipgloss.NewStyle().Foreground(grayLo)
	styleBody   = lipgloss.NewStyle().Foreground(white)
	styleEmph   = lipgloss.NewStyle().Foreground(white).Bold(true)
	styleLink   = lipgloss.NewStyle().Foreground(cyan).Underline(true)
	styleKey    = lipgloss.NewStyle().Foreground(accentHi).Bold(true)
	styleValue  = lipgloss.NewStyle().Foreground(whiteDim)

	styleHeader = lipgloss.NewStyle().
			Foreground(accentHi).Bold(true).
			Padding(0, 1)

	styleBrand = lipgloss.NewStyle().
			Foreground(accentHi).Bold(true)

	// Cards / Boxes --------------------------------------------------------

	styleUserBox = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(cyan).
			Padding(0, 1).
			MarginTop(1)

	styleAssistantBox = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(accent).
				Padding(0, 1).
				MarginTop(1)

	styleToolBox = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(gray).
			Padding(0, 1).
			MarginTop(1)

	styleToolBoxRunning = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(yellow).
				Padding(0, 1).
				MarginTop(1)

	styleToolBoxOK = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(green).
			Padding(0, 1).
			MarginTop(1)

	styleToolBoxErr = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(red).
				Padding(0, 1).
				MarginTop(1)

	styleSidebar = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(grayLo).
			Padding(1, 1)

	styleSidebarSel = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(accentHi).
			Background(bg2).
			Padding(0, 1)

	styleSystem = lipgloss.NewStyle().Foreground(grayHi).Italic(true)
	styleError  = lipgloss.NewStyle().Foreground(redHi).Bold(true)
	styleWarn   = lipgloss.NewStyle().Foreground(yellow)
	styleOk     = lipgloss.NewStyle().Foreground(green).Bold(true)

	styleStatusBar = lipgloss.NewStyle().
			Foreground(white).
			Background(bg2).
			Padding(0, 1)

	styleStatusBarKey = lipgloss.NewStyle().
				Foreground(accentHi).Bold(true)

	styleComposer = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(accent).
			Padding(0, 1)

	styleComposerFocused = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(accentHi).
				Padding(0, 1)

	styleComposerBash = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(green).
				Padding(0, 1)

	styleOverlay = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(accent).
			Padding(1, 2)

	stylePanel = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(grayLo).
			Padding(0, 1)

	styleCodeBlock = lipgloss.NewStyle().
			Background(bg1).
			Foreground(white).
			Padding(0, 1)

	styleQuote = lipgloss.NewStyle().
			Foreground(grayHi).Italic(true)

	// Diff styles
	styleDiffHunk = lipgloss.NewStyle().Foreground(diffHunkFg).Bold(true)
	styleDiffAdd  = lipgloss.NewStyle().
			Background(diffAddBg).
			Foreground(diffAddFg)
	styleDiffDel = lipgloss.NewStyle().
			Background(diffDelBg).
			Foreground(diffDelFg)

	// Pill / badge styles (returned by chip helper)
	stylePill = lipgloss.NewStyle().
			Padding(0, 1).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(grayLo)
)

// chip renders a small inline badge.
func chip(kind, label string) string {
	style := stylePill
	switch kind {
	case "evidence", "ok", "pass", "verified":
		style = style.Background(lipgloss.Color("#10321D")).Foreground(green).BorderForeground(green)
	case "unknown", "warn", "risk":
		style = style.Background(lipgloss.Color("#3A2E0A")).Foreground(yellow).BorderForeground(yellow)
	case "claim", "info":
		style = style.Background(lipgloss.Color("#1E1B4B")).Foreground(accentHi).BorderForeground(accentLo)
	case "err", "fail", "danger":
		style = style.Background(lipgloss.Color("#3B1313")).Foreground(redHi).BorderForeground(red)
	case "tool":
		style = style.Background(bg3).Foreground(white).BorderForeground(grayLo)
	case "user":
		style = style.Background(lipgloss.Color("#0F2438")).Foreground(cyan).BorderForeground(cyan)
	case "assistant":
		style = style.Background(lipgloss.Color("#1F1B36")).Foreground(accentHi).BorderForeground(accent)
	}
	return style.Render(label)
}

// Cached styles used on the hot render path. Avoids allocating a fresh
// lipgloss.Style on every View() call.
var (
	styleHeaderRow    = lipgloss.NewStyle().Padding(0, 1)
	styleCodeLineNum  = lipgloss.NewStyle().Foreground(grayLo)
	styleDiffCtxLine  = lipgloss.NewStyle().Foreground(whiteDim)
	styleDiffFileHdr  = lipgloss.NewStyle().Foreground(cyan).Bold(true)
	styleOverlayLeft  = func(w int) lipgloss.Style { return lipgloss.NewStyle().Width(w) }
	styleOverlayRight = func(w int) lipgloss.Style { return lipgloss.NewStyle().Width(w) }
)
