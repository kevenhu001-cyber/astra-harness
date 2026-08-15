package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type slashCmd struct {
	Name string
	Desc string
}

var slashCommands = []slashCmd{
	{"/help", "show keybindings and commands"},
	{"/status", "show compiled knowledge state"},
	{"/goal", "set or view the active goal"},
	{"/claims", "list claims"},
	{"/unknowns", "list ranked unknowns"},
	{"/evidence", "list evidence"},
	{"/actions", "list recent actions"},
	{"/model", "switch model"},
	{"/provider", "switch provider"},
	{"/permissions", "set permission mode (ask|allow|deny)"},
	{"/plan", "toggle plan mode"},
	{"/init", "initialize project index"},
	{"/index", "rebuild knowledge index"},
	{"/verify", "run tests/build and record evidence"},
	{"/compact", "compact conversation context"},
	{"/diff", "show git diff"},
	{"/sessions", "list saved sessions"},
	{"/resume", "resume a session"},
	{"/export", "export transcript to markdown"},
	{"/debug", "show config and state paths"},
	{"/cost", "show token usage"},
	{"/clear", "clear the chat view"},
	{"/new", "start a new session"},
	{"/quit", "exit Astra"},
}

type composer struct {
	ta       textarea.Model
	filtered []slashCmd
	sel      int
	show     bool
	focused  bool
	history  []string
	histIdx  int
	plain    bool
}

func newComposer(width int) composer {
	ta := textarea.New()
	ta.Placeholder = "Describe a task, ask a question, or type / for commands  (Alt+Enter for newline)"
	ta.Prompt = "❯ "
	ta.ShowLineNumbers = false
	ta.CharLimit = 0
	ta.SetWidth(width - 4)
	ta.SetHeight(2)
	ta.Focus()
	return composer{ta: ta, focused: true}
}

func (c *composer) SetWidth(w int) {
	c.ta.SetWidth(w - 4)
}

func (c *composer) Value() string {
	return c.ta.Value()
}

func (c *composer) SetValue(v string) {
	c.ta.SetValue(v)
}

func (c *composer) Focus() {
	c.focused = true
	c.ta.Focus()
}

func (c *composer) Blur() {
	c.focused = false
	c.ta.Blur()
}

// update returns (text, submit) when the user submits, and whether the key
// was handled at the composer level.
func (c *composer) update(msg tea.Msg) (string, bool, bool) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		c.ta, _ = c.ta.Update(msg)
		return "", false, false
	}
	s := key.String()
	value := c.ta.Value()
	if !c.plain {
		c.refreshSlash(value)
	}

	if c.show {
		switch s {
		case "up":
			c.sel = (c.sel - 1 + len(c.filtered)) % len(c.filtered)
			return "", false, true
		case "down":
			c.sel = (c.sel + 1) % len(c.filtered)
			return "", false, true
		case "tab", "enter", "ctrl+j":
			if len(c.filtered) > 0 {
				c.complete(c.filtered[c.sel].Name)
				if s == "enter" || s == "ctrl+j" {
					// First completion, second enter submits.
					c.show = false
					return "", false, true
				}
				return "", false, true
			}
		case "esc":
			c.show = false
			return "", false, true
		}
	}

	switch s {
	case "enter", "ctrl+j":
		if strings.TrimSpace(value) == "" {
			return "", false, true
		}
		c.pushHistory(value)
		out := value
		c.ta.SetValue("")
		c.show = false
		return out, true, true
	case "alt+enter", "alt+ctrl+j":
		c.ta.InsertString("\n")
		return "", false, true
	case "ctrl+up":
		if len(c.history) > 0 {
			if c.histIdx == 0 {
				c.histIdx = len(c.history)
			}
			c.histIdx--
			c.ta.SetValue(c.history[c.histIdx])
		}
		return "", false, true
	case "ctrl+down":
		if c.histIdx < len(c.history) {
			c.histIdx++
			if c.histIdx == len(c.history) {
				c.ta.SetValue("")
			} else {
				c.ta.SetValue(c.history[c.histIdx])
			}
		}
		return "", false, true
	case "esc":
		if c.show {
			c.show = false
			return "", false, true
		}
	}
	c.ta, _ = c.ta.Update(msg)
	if !c.plain {
		c.refreshSlash(c.ta.Value())
	}
	return "", false, false
}

func (c *composer) refreshSlash(value string) {
	if !strings.HasPrefix(value, "/") || strings.ContainsAny(value, " \t") {
		c.show = false
		return
	}
	prefix := strings.ToLower(value)
	c.filtered = c.filtered[:0]
	for _, cmd := range slashCommands {
		if strings.HasPrefix(strings.ToLower(cmd.Name), prefix) {
			c.filtered = append(c.filtered, cmd)
		}
	}
	c.show = len(c.filtered) > 0
	if c.sel >= len(c.filtered) {
		c.sel = 0
	}
}

func (c *composer) complete(name string) {
	value := c.ta.Value()
	idx := strings.IndexByte(value, ' ')
	if idx >= 0 {
		value = name + value[idx:]
	} else {
		value = name + " "
	}
	c.ta.SetValue(value)
	c.show = false
}

func (c *composer) pushHistory(v string) {
	if len(c.history) == 0 || c.history[len(c.history)-1] != v {
		c.history = append(c.history, v)
		if len(c.history) > 100 {
			c.history = c.history[len(c.history)-100:]
		}
	}
	c.histIdx = len(c.history)
}

func (c *composer) View(width int) string {
	inner := c.ta.View()
	box := styleComposer.Width(width - 2).Render(inner)
	if !c.show {
		return box
	}
	var b strings.Builder
	b.WriteString(box)
	b.WriteString("\n")
	popWidth := 60
	if width-4 < popWidth {
		popWidth = width - 4
	}
	for i, cmd := range c.filtered {
		line := cmd.Name + "  " + styleDim.Render(cmd.Desc)
		if i == c.sel {
			line = styleTitle.Render("● "+cmd.Name) + "  " + styleDim.Render(cmd.Desc)
		}
		b.WriteString(lipgloss.NewStyle().Width(popWidth).Padding(0, 1).Render(line))
		b.WriteString("\n")
	}
	b.WriteString(styleDim.Render("↑↓ choose · ⏎ select · esc close"))
	return b.String()
}
