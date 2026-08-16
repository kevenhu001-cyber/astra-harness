package tui

import (
	"sort"
	"sync"

	"github.com/charmbracelet/lipgloss"
)

// themePalette describes the active color set. All rendered styles are derived
// from it. Switching themes rebuilds the styles in place.
type themePalette struct {
	Name string

	// Surfaces
	Bg0 lipgloss.Color
	Bg1 lipgloss.Color
	Bg2 lipgloss.Color
	Bg3 lipgloss.Color

	// Accents
	Accent   lipgloss.Color
	AccentHi lipgloss.Color
	AccentLo lipgloss.Color

	// Semantic
	Cyan     lipgloss.Color
	CyanHi   lipgloss.Color
	Magenta  lipgloss.Color
	Green    lipgloss.Color
	Yellow   lipgloss.Color
	Orange   lipgloss.Color
	Red      lipgloss.Color
	RedHi    lipgloss.Color
	Gray     lipgloss.Color
	GrayLo   lipgloss.Color
	GrayHi   lipgloss.Color
	White    lipgloss.Color
	WhiteDim lipgloss.Color

	// Diff
	DiffAddBg  lipgloss.Color
	DiffAddFg  lipgloss.Color
	DiffDelBg  lipgloss.Color
	DiffDelFg  lipgloss.Color
	DiffCtxFg  lipgloss.Color
	DiffHunkFg lipgloss.Color

	// Pills
	PillEvidence lipgloss.Color
	PillUnknown  lipgloss.Color
	PillClaim    lipgloss.Color
	PillError    lipgloss.Color
}

var palettes = map[string]themePalette{
	"astra-dark": {
		Name: "astra-dark",
		Bg0:  "#0F0F17", Bg1: "#15151F", Bg2: "#1C1C29", Bg3: "#262638",
		Accent: "#8B7CF6", AccentHi: "#B4A7FF", AccentLo: "#5B4DC9",
		Cyan: "#5FD4FF", CyanHi: "#9CE5FF", Magenta: "#E879F9",
		Green: "#4ADE80", Yellow: "#FACC15", Orange: "#FB923C",
		Red: "#F87171", RedHi: "#FCA5A5",
		Gray: "#6B7280", GrayLo: "#3F3F55", GrayHi: "#9CA3AF",
		White: "#E5E7EB", WhiteDim: "#A0A0B5",
		DiffAddBg: "#0F2E1F", DiffAddFg: "#7FE3A6",
		DiffDelBg: "#3B1313", DiffDelFg: "#FCA5A5",
		DiffCtxFg: "#7A7A91", DiffHunkFg: "#5FD4FF",
		PillEvidence: "#10321D", PillUnknown: "#3A2E0A",
		PillClaim: "#1E1B4B", PillError: "#3B1313",
	},
	"astra-light": {
		Name: "astra-light",
		Bg0:  "#FFFFFF", Bg1: "#F6F6F9", Bg2: "#EAEAF2", Bg3: "#D6D6E2",
		Accent: "#5B4DC9", AccentHi: "#3F37A4", AccentLo: "#7A6AD9",
		Cyan: "#0EA5E9", CyanHi: "#0284C7", Magenta: "#C026D3",
		Green: "#16A34A", Yellow: "#CA8A04", Orange: "#EA580C",
		Red: "#DC2626", RedHi: "#EF4444",
		Gray: "#6B7280", GrayLo: "#9CA3AF", GrayHi: "#4B5563",
		White: "#1F2937", WhiteDim: "#374151",
		DiffAddBg: "#DCFCE7", DiffAddFg: "#166534",
		DiffDelBg: "#FEE2E2", DiffDelFg: "#991B1B",
		DiffCtxFg: "#6B7280", DiffHunkFg: "#0EA5E9",
		PillEvidence: "#DCFCE7", PillUnknown: "#FEF3C7",
		PillClaim: "#EDE9FE", PillError: "#FEE2E2",
	},
	"mono": {
		Name: "mono",
		Bg0:  "#000000", Bg1: "#0A0A0A", Bg2: "#141414", Bg3: "#1F1F1F",
		Accent: "#E5E7EB", AccentHi: "#FFFFFF", AccentLo: "#9CA3AF",
		Cyan: "#D4D4D8", CyanHi: "#E5E7EB", Magenta: "#A1A1AA",
		Green: "#A1A1AA", Yellow: "#D4D4D8", Orange: "#E5E7EB",
		Red: "#E5E7EB", RedHi: "#FFFFFF",
		Gray: "#52525B", GrayLo: "#27272A", GrayHi: "#71717A",
		White: "#E5E7EB", WhiteDim: "#A1A1AA",
		DiffAddBg: "#1F1F1F", DiffAddFg: "#E5E7EB",
		DiffDelBg: "#27272A", DiffDelFg: "#FFFFFF",
		DiffCtxFg: "#71717A", DiffHunkFg: "#D4D4D8",
		PillEvidence: "#1F1F1F", PillUnknown: "#27272A",
		PillClaim: "#141414", PillError: "#27272A",
	},
	"codex": {
		Name: "codex",
		Bg0:  "#000000", Bg1: "#0A0A0A", Bg2: "#161616", Bg3: "#1F1F1F",
		Accent: "#F97316", AccentHi: "#FFA94D", AccentLo: "#9A3412",
		Cyan: "#E5E5E5", CyanHi: "#FFFFFF", Magenta: "#A3A3A3",
		Green: "#FFFFFF", Yellow: "#F97316", Orange: "#F97316",
		Red: "#F97316", RedHi: "#FFB27A",
		Gray: "#737373", GrayLo: "#262626", GrayHi: "#A3A3A3",
		White: "#E5E5E5", WhiteDim: "#A3A3A3",
		DiffAddBg: "#1A1A1A", DiffAddFg: "#FFFFFF",
		DiffDelBg: "#2A1608", DiffDelFg: "#FFB27A",
		DiffCtxFg: "#737373", DiffHunkFg: "#F97316",
		PillEvidence: "#111111", PillUnknown: "#1A0E04",
		PillClaim: "#0D0D0D", PillError: "#2A1608",
	},
}

var (
	themeMu sync.RWMutex
	active  = palettes["codex"]
)

// ThemeNames returns the registered theme names (sorted).
func ThemeNames() []string {
	themeMu.RLock()
	defer themeMu.RUnlock()
	out := make([]string, 0, len(palettes))
	for k := range palettes {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// CurrentTheme returns the active theme name.
func CurrentTheme() string {
	themeMu.RLock()
	defer themeMu.RUnlock()
	return active.Name
}

// SetTheme switches the active theme. Returns the applied name (matched
// against the registry). When the name is unknown the active theme is
// unchanged and the call returns "". All affected styles are rebuilt.
func SetTheme(name string) string {
	themeMu.Lock()
	defer themeMu.Unlock()
	next, ok := palettes[name]
	if !ok {
		return ""
	}
	active = next
	rebuildStylesLocked()
	return active.Name
}

// activePalette returns the current palette (caller is expected to not mutate).
func activePalette() themePalette {
	themeMu.RLock()
	defer themeMu.RUnlock()
	return active
}

// Style palette accessors (rebuilders use these via color keys).
var paletteKeys = map[string]func(p themePalette) lipgloss.Color{
	"accent":     func(p themePalette) lipgloss.Color { return p.Accent },
	"accentHi":   func(p themePalette) lipgloss.Color { return p.AccentHi },
	"accentLo":   func(p themePalette) lipgloss.Color { return p.AccentLo },
	"cyan":       func(p themePalette) lipgloss.Color { return p.Cyan },
	"cyanHi":     func(p themePalette) lipgloss.Color { return p.CyanHi },
	"magenta":    func(p themePalette) lipgloss.Color { return p.Magenta },
	"green":      func(p themePalette) lipgloss.Color { return p.Green },
	"yellow":     func(p themePalette) lipgloss.Color { return p.Yellow },
	"orange":     func(p themePalette) lipgloss.Color { return p.Orange },
	"red":        func(p themePalette) lipgloss.Color { return p.Red },
	"redHi":      func(p themePalette) lipgloss.Color { return p.RedHi },
	"gray":       func(p themePalette) lipgloss.Color { return p.Gray },
	"grayLo":     func(p themePalette) lipgloss.Color { return p.GrayLo },
	"grayHi":     func(p themePalette) lipgloss.Color { return p.GrayHi },
	"white":      func(p themePalette) lipgloss.Color { return p.White },
	"whiteDim":   func(p themePalette) lipgloss.Color { return p.WhiteDim },
	"diffAddBg":  func(p themePalette) lipgloss.Color { return p.DiffAddBg },
	"diffAddFg":  func(p themePalette) lipgloss.Color { return p.DiffAddFg },
	"diffDelBg":  func(p themePalette) lipgloss.Color { return p.DiffDelBg },
	"diffDelFg":  func(p themePalette) lipgloss.Color { return p.DiffDelFg },
	"diffCtxFg":  func(p themePalette) lipgloss.Color { return p.DiffCtxFg },
	"diffHunkFg": func(p themePalette) lipgloss.Color { return p.DiffHunkFg },
	"bg1":        func(p themePalette) lipgloss.Color { return p.Bg1 },
	"bg2":        func(p themePalette) lipgloss.Color { return p.Bg2 },
	"bg3":        func(p themePalette) lipgloss.Color { return p.Bg3 },
	"pillEv":     func(p themePalette) lipgloss.Color { return p.PillEvidence },
	"pillUn":     func(p themePalette) lipgloss.Color { return p.PillUnknown },
	"pillCl":     func(p themePalette) lipgloss.Color { return p.PillClaim },
	"pillErr":    func(p themePalette) lipgloss.Color { return p.PillError },
}

func (pal themePalette) access(name string) lipgloss.Color {
	if k, ok := paletteKeys[name]; ok {
		return k(pal)
	}
	return pal.Accent
}

// Styles (rebuilt by rebuildStyles on theme switch). Other files reference
// these by name; the values mutate in place when the theme changes.
var (
	styleTitle           lipgloss.Style
	styleSubtle          lipgloss.Style
	styleDim             lipgloss.Style
	styleFaint           lipgloss.Style
	styleBody            lipgloss.Style
	styleEmph            lipgloss.Style
	styleLink            lipgloss.Style
	styleKey             lipgloss.Style
	styleValue           lipgloss.Style
	styleHeader          lipgloss.Style
	styleBrand           lipgloss.Style
	styleUserBox         lipgloss.Style
	styleAssistantBox    lipgloss.Style
	styleToolBox         lipgloss.Style
	styleToolBoxRunning  lipgloss.Style
	styleToolBoxOK       lipgloss.Style
	styleToolBoxErr      lipgloss.Style
	styleSidebar         lipgloss.Style
	styleSidebarSel      lipgloss.Style
	styleSystem          lipgloss.Style
	styleError           lipgloss.Style
	styleWarn            lipgloss.Style
	styleOk              lipgloss.Style
	styleStatusBar       lipgloss.Style
	styleStatusBarKey    lipgloss.Style
	styleComposer        lipgloss.Style
	styleComposerFocused lipgloss.Style
	styleComposerBash    lipgloss.Style
	styleOverlay         lipgloss.Style
	stylePanel           lipgloss.Style
	styleCodeBlock       lipgloss.Style
	styleQuote           lipgloss.Style
	styleDiffHunk        lipgloss.Style
	styleDiffAdd         lipgloss.Style
	styleDiffDel         lipgloss.Style
	stylePill            lipgloss.Style
	styleHeaderRow       lipgloss.Style
	styleCodeLineNum     lipgloss.Style
	styleDiffCtxLine     lipgloss.Style
	styleDiffFileHdr     lipgloss.Style
)

// rebuildStylesLocked rebuilds all style globals from the active palette.
// Caller must hold themeMu for writing.
func rebuildStylesLocked() {
	pal := active
	styleTitle = lipgloss.NewStyle().Foreground(pal.AccentHi).Bold(true)
	styleSubtle = lipgloss.NewStyle().Foreground(pal.GrayHi)
	styleDim = lipgloss.NewStyle().Foreground(pal.Gray)
	styleFaint = lipgloss.NewStyle().Foreground(pal.GrayLo)
	styleBody = lipgloss.NewStyle().Foreground(pal.White)
	styleEmph = lipgloss.NewStyle().Foreground(pal.White).Bold(true)
	styleLink = lipgloss.NewStyle().Foreground(pal.Cyan).Underline(true)
	styleKey = lipgloss.NewStyle().Foreground(pal.AccentHi).Bold(true)
	styleValue = lipgloss.NewStyle().Foreground(pal.WhiteDim)
	styleHeader = lipgloss.NewStyle().Foreground(pal.AccentHi).Bold(true).Padding(0, 1)
	styleBrand = lipgloss.NewStyle().Foreground(pal.AccentHi).Bold(true)

	styleUserBox = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(pal.Cyan).
		Padding(0, 1).
		MarginTop(1)
	styleAssistantBox = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(pal.Accent).
		Padding(0, 1).
		MarginTop(1)
	styleToolBox = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(pal.Gray).
		Padding(0, 1).
		MarginTop(1)
	styleToolBoxRunning = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(pal.Yellow).
		Padding(0, 1).
		MarginTop(1)
	styleToolBoxOK = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(pal.Green).
		Padding(0, 1).
		MarginTop(1)
	styleToolBoxErr = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(pal.Red).
		Padding(0, 1).
		MarginTop(1)

	styleSidebar = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(pal.GrayLo).
		Padding(1, 1)
	styleSidebarSel = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(pal.AccentHi).
		Background(pal.Bg2).
		Padding(0, 1)

	styleSystem = lipgloss.NewStyle().Foreground(pal.GrayHi).Italic(true)
	styleError = lipgloss.NewStyle().Foreground(pal.RedHi).Bold(true)
	styleWarn = lipgloss.NewStyle().Foreground(pal.Yellow)
	styleOk = lipgloss.NewStyle().Foreground(pal.Green).Bold(true)

	styleStatusBar = lipgloss.NewStyle().Foreground(pal.White).Background(pal.Bg2).Padding(0, 1)
	styleStatusBarKey = lipgloss.NewStyle().Foreground(pal.AccentHi).Bold(true)

	styleComposer = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(pal.Accent).
		Padding(0, 1)
	styleComposerFocused = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(pal.AccentHi).
		Padding(0, 1)
	styleComposerBash = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(pal.Green).
		Padding(0, 1)
	styleOverlay = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(pal.Accent).
		Padding(1, 2)
	stylePanel = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(pal.GrayLo).
		Padding(0, 1)
	styleCodeBlock = lipgloss.NewStyle().
		Background(pal.Bg1).
		Foreground(pal.White).
		Padding(0, 1)
	styleQuote = lipgloss.NewStyle().Foreground(pal.GrayHi).Italic(true)

	styleDiffHunk = lipgloss.NewStyle().Foreground(pal.DiffHunkFg).Bold(true)
	styleDiffAdd = lipgloss.NewStyle().Background(pal.DiffAddBg).Foreground(pal.DiffAddFg)
	styleDiffDel = lipgloss.NewStyle().Background(pal.DiffDelBg).Foreground(pal.DiffDelFg)
	stylePill = lipgloss.NewStyle().Padding(0, 1).Border(lipgloss.RoundedBorder()).BorderForeground(pal.GrayLo)

	styleHeaderRow = lipgloss.NewStyle().Padding(0, 1)
	styleCodeLineNum = lipgloss.NewStyle().Foreground(pal.GrayLo)
	styleDiffCtxLine = lipgloss.NewStyle().Foreground(pal.WhiteDim)
	styleDiffFileHdr = lipgloss.NewStyle().Foreground(pal.Cyan).Bold(true)

	mdCache = sync.Map{}
}

func init() {
	rebuildStylesLocked()
}

// chip renders a small inline badge. Colors come from the live palette.
func chip(kind, label string) string {
	pal := activePalette()
	style := stylePill
	switch kind {
	case "evidence", "ok", "pass", "verified":
		style = style.Background(pal.PillEvidence).Foreground(pal.Green).BorderForeground(pal.Green)
	case "unknown", "warn", "risk":
		style = style.Background(pal.PillUnknown).Foreground(pal.Yellow).BorderForeground(pal.Yellow)
	case "claim", "info":
		style = style.Background(pal.PillClaim).Foreground(pal.AccentHi).BorderForeground(pal.AccentLo)
	case "err", "fail", "danger":
		style = style.Background(pal.PillError).Foreground(pal.RedHi).BorderForeground(pal.Red)
	case "tool":
		style = style.Background(pal.Bg3).Foreground(pal.White).BorderForeground(pal.GrayLo)
	case "user":
		style = style.Background(pal.Bg1).Foreground(pal.Cyan).BorderForeground(pal.Cyan)
	case "assistant":
		style = style.Background(pal.Bg2).Foreground(pal.AccentHi).BorderForeground(pal.Accent)
	}
	return style.Render(label)
}

// keep import used (clipboard isn't currently pulled but the helper file is
// imported via underscore elsewhere; this guards against accidental removal).
var _ = lipgloss.NewStyle
