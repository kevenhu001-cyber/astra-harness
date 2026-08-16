package tui

import (
	"sort"
	"sync"

	"github.com/charmbracelet/lipgloss"
)

// themePalette describes the active color set. All rendered styles are derived
// from it. Switching themes rebuilds the styles in place.
//
// The palette follows the Codex style guide (codex-rs/tui/styles.md):
//   - Headers: bold, primary text color
//   - Secondary text: dim
//   - User input tips, selection, status indicators: cyan
//   - Success and additions: green
//   - Errors, failures and deletions: red
//   - Brand ("Codex" / "$" prompt): magenta
//   - Avoid custom colors that clash with terminal themes
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

	// Codex conversation rendering
	UserBg lipgloss.Color // subtle tint behind user messages (blend of fg over bg)
	CmdFg  lipgloss.Color // command / tool names (blue in Codex)
	TextFg lipgloss.Color // primary body text

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
	// Default theme: Catppuccin Mocha, matching the colors Codex uses for
	// exec cells and tool calls (e.g. command names in #89B4FA, body text in
	// #CDD6F4, from codex-rs/tui/src/exec_cell snapshots).
	"codex": {
		Name: "codex",
		Bg0:  "#1E1E2E", Bg1: "#181825", Bg2: "#313244", Bg3: "#45475A",
		Accent: "#89DCEB", AccentHi: "#CDD6F4", AccentLo: "#6C7086",
		Cyan: "#89DCEB", CyanHi: "#A6E3FF", Magenta: "#F5C2E7",
		Green: "#A6E3A1", Yellow: "#F9E2AF", Orange: "#FAB387",
		Red: "#F38BA8", RedHi: "#F5B8C6",
		Gray: "#6C7086", GrayLo: "#45475A", GrayHi: "#9399B2",
		White: "#CDD6F4", WhiteDim: "#A6ADC8",
		UserBg: "#393A4A", CmdFg: "#89B4FA", TextFg: "#CDD6F4",
		DiffAddBg: "#26402F", DiffAddFg: "#A6E3A1",
		DiffDelBg: "#452A35", DiffDelFg: "#F38BA8",
		DiffCtxFg: "#6C7086", DiffHunkFg: "#89B4FA",
		PillEvidence: "#24372A", PillUnknown: "#3A3A24",
		PillClaim: "#2A3A4A", PillError: "#452A35",
	},
	"astra-dark": {
		Name: "astra-dark",
		Bg0:  "#0F0F17", Bg1: "#15151F", Bg2: "#1C1C29", Bg3: "#262638",
		Accent: "#5FD4FF", AccentHi: "#E5E7EB", AccentLo: "#3F3F55",
		Cyan: "#5FD4FF", CyanHi: "#9CE5FF", Magenta: "#E879F9",
		Green: "#4ADE80", Yellow: "#FACC15", Orange: "#FB923C",
		Red: "#F87171", RedHi: "#FCA5A5",
		Gray: "#6B7280", GrayLo: "#3F3F55", GrayHi: "#9CA3AF",
		White: "#E5E7EB", WhiteDim: "#A0A0B5",
		UserBg: "#1E1E2C", CmdFg: "#7DD3FC", TextFg: "#E5E7EB",
		DiffAddBg: "#0F2E1F", DiffAddFg: "#7FE3A6",
		DiffDelBg: "#3B1313", DiffDelFg: "#FCA5A5",
		DiffCtxFg: "#7A7A91", DiffHunkFg: "#5FD4FF",
		PillEvidence: "#10321D", PillUnknown: "#3A2E0A",
		PillClaim: "#1E1B4B", PillError: "#3B1313",
	},
	"astra-light": {
		Name: "astra-light",
		Bg0:  "#FFFFFF", Bg1: "#F6F6F9", Bg2: "#EAEAF2", Bg3: "#D6D6E2",
		Accent: "#0EA5E9", AccentHi: "#1F2937", AccentLo: "#9CA3AF",
		Cyan: "#0EA5E9", CyanHi: "#0284C7", Magenta: "#C026D3",
		Green: "#16A34A", Yellow: "#CA8A04", Orange: "#EA580C",
		Red: "#DC2626", RedHi: "#EF4444",
		Gray: "#6B7280", GrayLo: "#9CA3AF", GrayHi: "#4B5563",
		White: "#1F2937", WhiteDim: "#374151",
		UserBg: "#F2F2F7", CmdFg: "#2563EB", TextFg: "#1F2937",
		DiffAddBg: "#DCFCE7", DiffAddFg: "#166534",
		DiffDelBg: "#FEE2E2", DiffDelFg: "#991B1B",
		DiffCtxFg: "#6B7280", DiffHunkFg: "#0EA5E9",
		PillEvidence: "#DCFCE7", PillUnknown: "#FEF3C7",
		PillClaim: "#EDE9FE", PillError: "#FEE2E2",
	},
	"mono": {
		Name: "mono",
		Bg0:  "#000000", Bg1: "#0A0A0A", Bg2: "#141414", Bg3: "#1F1F1F",
		Accent: "#E5E7EB", AccentHi: "#FFFFFF", AccentLo: "#52525B",
		Cyan: "#D4D4D8", CyanHi: "#E5E7EB", Magenta: "#A1A1AA",
		Green: "#A1A1AA", Yellow: "#D4D4D8", Orange: "#E5E7EB",
		Red: "#E5E7EB", RedHi: "#FFFFFF",
		Gray: "#52525B", GrayLo: "#27272A", GrayHi: "#71717A",
		White: "#E5E7EB", WhiteDim: "#A1A1AA",
		UserBg: "#101010", CmdFg: "#D4D4D8", TextFg: "#E5E7EB",
		DiffAddBg: "#1F1F1F", DiffAddFg: "#E5E7EB",
		DiffDelBg: "#27272A", DiffDelFg: "#FFFFFF",
		DiffCtxFg: "#71717A", DiffHunkFg: "#D4D4D8",
		PillEvidence: "#1F1F1F", PillUnknown: "#27272A",
		PillClaim: "#141414", PillError: "#27272A",
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
	"userBg":     func(p themePalette) lipgloss.Color { return p.UserBg },
	"cmdFg":      func(p themePalette) lipgloss.Color { return p.CmdFg },
	"textFg":     func(p themePalette) lipgloss.Color { return p.TextFg },
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

	// Codex conversation-rendering styles.
	styleVerb     lipgloss.Style // "Running / Ran / Called / You ran" verbs (bold)
	styleCmdName  lipgloss.Style // command / tool names (blue)
	styleBullet   lipgloss.Style // "•" status bullet (dim)
	stylePrefix   lipgloss.Style // "│" / "└" continuation prefixes (dim)
	styleOutput   lipgloss.Style // tool/command output body
	stylePrompt   lipgloss.Style // "$" shell prompt (magenta)
	styleExitOK   lipgloss.Style // "✓" success marker (green)
	styleExitErr  lipgloss.Style // "✗" failure marker (red, bold)
	styleSep      lipgloss.Style // "────" turn separator (dim)
	styleUserMsg  lipgloss.Style // user message tint block (Codex user_message_style)
	styleUserPre  lipgloss.Style // "› " user message line prefix (bold dim)
	styleCodeLine lipgloss.Style // inline code span inside exec-cell output
)

// rebuildStylesLocked rebuilds all style globals from the active palette.
// Caller must hold themeMu for writing.
//
// Conventions (Codex styles.md):
//   - Headers: bold, primary color
//   - Secondary text: dim
//   - Tips / selection / status indicators: cyan
//   - Success / additions: green; errors / deletions: red
//   - Brand and "$" prompt: magenta
//   - No decorative boxes around messages: user text gets a subtle
//     background tint, assistant text renders as plain markdown, and tool
//     calls render as Codex exec cells with "•" bullets and "│"/"└" prefixes.
func rebuildStylesLocked() {
	pal := active
	styleTitle = lipgloss.NewStyle().Foreground(pal.White).Bold(true)
	styleSubtle = lipgloss.NewStyle().Foreground(pal.GrayHi)
	styleDim = lipgloss.NewStyle().Foreground(pal.Gray)
	styleFaint = lipgloss.NewStyle().Foreground(pal.GrayLo)
	styleBody = lipgloss.NewStyle().Foreground(pal.TextFg)
	styleEmph = lipgloss.NewStyle().Foreground(pal.White).Bold(true)
	styleLink = lipgloss.NewStyle().Foreground(pal.Cyan).Underline(true)
	styleKey = lipgloss.NewStyle().Foreground(pal.Accent).Bold(true)
	styleValue = lipgloss.NewStyle().Foreground(pal.WhiteDim)
	styleHeader = lipgloss.NewStyle().Foreground(pal.White).Bold(true).Padding(0, 1)
	styleBrand = lipgloss.NewStyle().Foreground(pal.Magenta).Bold(true)

	// Messages render as Codex-style plain surfaces: user messages carry a
	// subtle background tint (user_message_bg = white 12% over bg), assistant
	// messages are box-free markdown, tool calls are exec cells. None of
	// these styles add padding or margins — layout comes from the renderers.
	styleUserMsg = lipgloss.NewStyle().Background(pal.UserBg)
	styleUserBox = styleUserMsg
	styleAssistantBox = lipgloss.NewStyle()
	styleToolBox = lipgloss.NewStyle()
	styleToolBoxRunning = styleToolBox
	styleToolBoxOK = styleToolBox
	styleToolBoxErr = styleToolBox

	styleSidebar = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(pal.GrayLo).
		Padding(1, 1)
	styleSidebarSel = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(pal.Accent).
		Background(pal.Bg2).
		Padding(0, 1)

	styleSystem = lipgloss.NewStyle().Foreground(pal.GrayHi).Italic(true)
	styleError = lipgloss.NewStyle().Foreground(pal.Red).Bold(true)
	styleWarn = lipgloss.NewStyle().Foreground(pal.Yellow)
	styleOk = lipgloss.NewStyle().Foreground(pal.Green).Bold(true)

	styleStatusBar = lipgloss.NewStyle().Foreground(pal.WhiteDim).Background(pal.Bg1).Padding(0, 1)
	styleStatusBarKey = lipgloss.NewStyle().Foreground(pal.Accent).Bold(true)

	// Composer is a borderless single-line input; focus is expressed by the
	// accent-colored "> " prompt and an underline rule (see composer.go).
	styleComposer = lipgloss.NewStyle().
		Padding(0, 0)
	styleComposerFocused = lipgloss.NewStyle().
		Padding(0, 0)
	styleComposerBash = lipgloss.NewStyle().
		Padding(0, 0)

	styleOverlay = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(pal.GrayLo).
		Padding(1, 2)
	stylePanel = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(pal.GrayLo).
		Padding(0, 1)
	styleCodeBlock = lipgloss.NewStyle().
		Background(pal.Bg1).
		Foreground(pal.TextFg).
		Padding(0, 1)
	styleQuote = lipgloss.NewStyle().Foreground(pal.GrayHi).Italic(true)

	styleDiffHunk = lipgloss.NewStyle().Foreground(pal.DiffHunkFg).Bold(true)
	styleDiffAdd = lipgloss.NewStyle().Background(pal.DiffAddBg).Foreground(pal.DiffAddFg)
	styleDiffDel = lipgloss.NewStyle().Background(pal.DiffDelBg).Foreground(pal.DiffDelFg)
	stylePill = lipgloss.NewStyle().Padding(0, 1).Border(lipgloss.RoundedBorder()).BorderForeground(pal.GrayLo)

	styleHeaderRow = lipgloss.NewStyle().Padding(0, 1)
	styleCodeLineNum = lipgloss.NewStyle().Foreground(pal.GrayLo)
	styleDiffCtxLine = lipgloss.NewStyle().Foreground(pal.WhiteDim)
	styleDiffFileHdr = lipgloss.NewStyle().Foreground(pal.CmdFg).Bold(true)

	// Codex conversation styles.
	styleVerb = lipgloss.NewStyle().Foreground(pal.White).Bold(true)
	styleCmdName = lipgloss.NewStyle().Foreground(pal.CmdFg)
	styleBullet = lipgloss.NewStyle().Foreground(pal.GrayHi)
	stylePrefix = lipgloss.NewStyle().Foreground(pal.GrayLo)
	styleOutput = lipgloss.NewStyle().Foreground(pal.WhiteDim)
	stylePrompt = lipgloss.NewStyle().Foreground(pal.Magenta).Bold(true)
	styleExitOK = lipgloss.NewStyle().Foreground(pal.Green)
	styleExitErr = lipgloss.NewStyle().Foreground(pal.Red).Bold(true)
	styleSep = lipgloss.NewStyle().Foreground(pal.GrayLo)
	styleUserPre = lipgloss.NewStyle().Faint(true).Bold(true)
	styleCodeLine = lipgloss.NewStyle().Foreground(pal.CmdFg)

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
