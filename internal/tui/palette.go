package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// paletteEntry describes one entry in the command palette.
type paletteEntry struct {
	id          string
	title       string
	desc        string
	category    string
	shortcut    string
	command     string // plain text command to run
	placeholder string // optional input placeholder
}

// palette is a fuzzy-search command palette modeled after Claude Code's ⌘K.
type palette struct {
	visible  bool
	input    string
	cursor   int
	filtered []paletteEntry
	height   int
	width    int
}

// builtinPaletteOnce caches the static palette entries so refilter does not
// re-allocate the full list on every keystroke.
var builtinPaletteOnce = builtinPaletteEntries

// builtinPaletteEntries returns the static palette actions. Dynamic entries
// (resume session x, switch to model y) are appended by the host app.
func builtinPaletteEntries() []paletteEntry {
	return []paletteEntry{
		{id: "new", title: "new task", desc: "start a fresh conversation", category: "Session", shortcut: "Ctrl+T", command: "/new"},
		{id: "resume", title: "resume session…", desc: "switch to a saved session", category: "Session", command: "/sessions"},
		{id: "clear", title: "clear chat", desc: "clear the visible chat view", category: "Session", shortcut: "Ctrl+L", command: "/clear"},
		{id: "export", title: "export transcript", desc: "save the chat as markdown", category: "Session", command: "/export"},
		{id: "compact", title: "compact context", desc: "rebuild context from knowledge state", category: "Session", command: "/compact"},

		{id: "status", title: "view status", desc: "show compiled knowledge state", category: "Knowledge", command: "/status"},
		{id: "goal", title: "view / set goal", desc: "manage the active goal", category: "Knowledge", command: "/goal"},
		{id: "claims", title: "view claims", desc: "list claims with confidence", category: "Knowledge", command: "/claims"},
		{id: "unknowns", title: "view unknowns", desc: "list ranked unknowns", category: "Knowledge", command: "/unknowns"},
		{id: "evidence", title: "view evidence", desc: "list captured evidence", category: "Knowledge", command: "/evidence"},
		{id: "actions", title: "view actions", desc: "recent actions + utility", category: "Knowledge", command: "/actions"},
		{id: "events", title: "view events", desc: "event sourcing log", category: "Knowledge", command: "/events"},

		{id: "model", title: "switch model", desc: "change the active LLM", category: "Model", command: "/model"},
		{id: "provider", title: "switch provider", desc: "change the active provider", category: "Model", command: "/provider"},
		{id: "config", title: "model settings…", desc: "edit base URL / API key / model ID", category: "Model", command: "/config"},
		{id: "cost", title: "cost & tokens", desc: "show usage and pricing", category: "Model", command: "/cost"},

		{id: "perm", title: "permission mode…", desc: "ask | allow | deny", category: "Safety", command: "/permissions ask"},
		{id: "plan", title: "toggle plan mode", desc: "block writes/execute", category: "Safety", command: "/plan"},
		{id: "undo", title: "undo last turn", desc: "rewind the last assistant turn", category: "Safety", shortcut: "Ctrl+U", command: "/undo"},

		{id: "verify", title: "run verification", desc: "tests + build, record evidence", category: "Build", command: "/verify"},
		{id: "init", title: "rebuild index", desc: "re-scan the repository", category: "Build", command: "/index"},
		{id: "diff", title: "view git diff", desc: "show uncommitted changes", category: "Build", command: "/diff"},
		{id: "commit", title: "git commit", desc: "commit pending changes with a message", category: "Build", command: "/commit", placeholder: "commit message"},

		{id: "help", title: "show help", desc: "keybindings and commands", category: "Help", shortcut: "F1 / ?", command: "/help"},
		{id: "debug", title: "debug info", desc: "paths, providers, config", category: "Help", command: "/debug"},
		{id: "theme", title: "theme…", desc: "switch theme (dark/light/auto)", category: "Help", command: "/theme"},
	}
}

// fuzzyMatch returns true if query matches the title (case-insensitive,
// permits each char of query to appear in order) — shared in util.go.

func (p *palette) Show() {
	p.visible = true
	p.input = ""
	p.cursor = 0
	all := builtinPaletteOnce()
	p.filtered = append(p.filtered[:0], all...)
}

func (p *palette) Hide() {
	p.visible = false
}

func (p *palette) SetSize(w, h int) {
	p.width = w
	p.height = h
}

func (p *palette) Update(msg tea.Msg) (tea.Cmd, bool /*submit*/) {
	if !p.visible {
		return nil, false
	}
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil, false
	}
	switch key.String() {
	case "esc", "ctrl+k":
		p.Hide()
		return nil, false
	case "enter":
		if len(p.filtered) == 0 {
			return nil, false
		}
		entry := p.filtered[p.cursor]
		p.Hide()
		return func() tea.Msg { return paletteSubmitMsg{entry: entry} }, true
	case "up":
		if len(p.filtered) > 0 {
			p.cursor = (p.cursor - 1 + len(p.filtered)) % len(p.filtered)
		}
		return nil, false
	case "down":
		if len(p.filtered) > 0 {
			p.cursor = (p.cursor + 1) % len(p.filtered)
		}
		return nil, false
	case "backspace":
		if len(p.input) > 0 {
			p.input = p.input[:len(p.input)-1]
			p.refilter()
		}
		return nil, false
	}
	if key.Type == tea.KeyRunes {
		p.input += key.String()
		p.refilter()
		return nil, false
	}
	return nil, false
}

func (p *palette) refilter() {
	all := builtinPaletteOnce()
	p.filtered = p.filtered[:0]
	for _, e := range all {
		if fuzzyMatch(p.input, e.title) || fuzzyMatch(p.input, e.id) || fuzzyMatch(p.input, e.category) {
			p.filtered = append(p.filtered, e)
		}
	}
	if p.cursor >= len(p.filtered) {
		p.cursor = 0
	}
}

func (p *palette) View() string {
	if !p.visible {
		return ""
	}
	w := p.width - 6
	if w > 90 {
		w = 90
	}
	if w < 40 {
		w = 40
	}
	var b strings.Builder
	b.WriteString(styleTitle.Render("⌘ Command Palette") + "  " + styleDim.Render("type to filter · ⏎ run · esc close"))
	b.WriteString("\n")
	b.WriteString(styleComposerFocused.Width(w).Render("  🔍 " + p.input + "▌"))
	b.WriteString("\n")
	b.WriteString(styleFaint.Render(strings.Repeat("─", w)))
	b.WriteString("\n")

	max := 12
	for i, e := range p.filtered {
		if i >= max {
			break
		}
		shortcut := ""
		if e.shortcut != "" {
			shortcut = styleDim.Render("  " + e.shortcut)
		}
		title := e.title
		if len(title) > 28 {
			title = title[:27] + "…"
		}
		desc := e.desc
		if len(desc) > 50 {
			desc = desc[:49] + "…"
		}
		line := fmtCat(e.category) + " " + styleBody.Render(padRight(title, 28)) + "  " + styleDim.Render(desc) + shortcut
		if i == p.cursor {
			line = fmtCat(e.category) + " " + styleTitle.Render("● "+padRight(title, 28)) + "  " + styleValue.Render(desc) + shortcut
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	if len(p.filtered) == 0 {
		b.WriteString(styleDim.Render("  no matching commands"))
	}
	if len(p.filtered) > max {
		b.WriteString(styleDim.Render(fmt.Sprintf("  …+%d more", len(p.filtered)-max)))
	}
	return lipgloss.Place(p.width, p.height, lipgloss.Center, lipgloss.Center,
		styleOverlay.Width(w).Render(b.String()))
}

func fmtCat(c string) string {
	pal := activePalette()
	color := pal.Gray
	switch c {
	case "Session":
		color = pal.Cyan
	case "Knowledge":
		color = pal.AccentHi
	case "Model":
		color = pal.Magenta
	case "Safety":
		color = pal.Red
	case "Build":
		color = pal.Green
	case "Files":
		color = pal.CyanHi
	case "Help":
		color = pal.Yellow
	}
	return lipgloss.NewStyle().Foreground(color).Bold(true).Render(padRight(c, 9))
}

// paletteSubmitMsg is dispatched when the user activates a palette entry.
type paletteSubmitMsg struct {
	entry paletteEntry
}
