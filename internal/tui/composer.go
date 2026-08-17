package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
	"github.com/rivo/uniseg"
)

type slashCmd struct {
	Name        string
	Desc        string
	Shortcut    string
	Category    string
	Placeholder string
}

var slashCategories = []string{"Session", "Knowledge", "Model", "Safety", "Build", "Files", "Help"}

var slashCommands = []slashCmd{
	{Name: "/help", Desc: "show keybindings and commands", Category: "Help", Shortcut: "F1"},
	{Name: "/status", Desc: "compiled knowledge state", Category: "Knowledge"},
	{Name: "/goal", Desc: "view or set the active goal", Category: "Knowledge", Placeholder: "goal description + criteria"},
	{Name: "/claims", Desc: "list claims", Category: "Knowledge"},
	{Name: "/unknowns", Desc: "list ranked unknowns", Category: "Knowledge"},
	{Name: "/evidence", Desc: "list evidence", Category: "Knowledge"},
	{Name: "/actions", Desc: "recent actions + utility", Category: "Knowledge"},
	{Name: "/events", Desc: "event sourcing log", Category: "Knowledge"},
	{Name: "/tree", Desc: "show project tree", Category: "Knowledge"},
	{Name: "/init", Desc: "initialize project", Category: "Build"},
	{Name: "/index", Desc: "rebuild knowledge index", Category: "Build"},
	{Name: "/verify", Desc: "run tests + build", Category: "Build"},
	{Name: "/commit", Desc: "git commit with message", Category: "Build", Placeholder: "commit message"},
	{Name: "/branch", Desc: "switch or create branch", Category: "Build", Placeholder: "branch name"},
	{Name: "/undo", Desc: "rewind last assistant turn", Category: "Safety", Shortcut: "Ctrl+U"},
	{Name: "/model", Desc: "switch model", Category: "Model"},
	{Name: "/provider", Desc: "switch provider", Category: "Model"},
	{Name: "/config", Desc: "connect a provider (API key / model)", Category: "Model", Shortcut: "Ctrl+M"},
	{Name: "/settings", Desc: "connect a provider (alias of /config)", Category: "Model"},
	{Name: "/set-url", Desc: "set provider base URL", Category: "Model", Placeholder: "<provider> <url>"},
	{Name: "/set-key", Desc: "set provider API key (saved locally)", Category: "Model", Placeholder: "<provider> <key>"},
	{Name: "/set-model", Desc: "add and switch to a model ID", Category: "Model", Placeholder: "<provider> <model>"},
	{Name: "/statusline", Desc: "configure footer status line items", Category: "Model", Placeholder: "model git-branch ..."},
	{Name: "/keymap", Desc: "view or remap core shortcuts", Category: "Help", Placeholder: "debug|reset"},
	{Name: "/cost", Desc: "token usage and cost", Category: "Model"},
	{Name: "/stats", Desc: "session statistics", Category: "Model"},
	{Name: "/permissions", Desc: "permission mode (ask|allow|deny)", Category: "Safety", Placeholder: "ask|allow|deny"},
	{Name: "/plan", Desc: "toggle plan mode", Category: "Safety"},
	{Name: "/add-file", Desc: "@ a file into the prompt", Category: "Files", Placeholder: "path"},
	{Name: "/diff", Desc: "show git diff", Category: "Build"},
	{Name: "/sessions", Desc: "list saved sessions", Category: "Session"},
	{Name: "/resume", Desc: "resume a session", Category: "Session", Placeholder: "session id"},
	{Name: "/compact", Desc: "compact conversation context", Category: "Session"},
	{Name: "/export", Desc: "export transcript as markdown", Category: "Session"},
	{Name: "/login", Desc: "sign in with your Astra account", Category: "Session"},
	{Name: "/logout", Desc: "sign out locally", Category: "Session"},
	{Name: "/whoami", Desc: "show the signed-in account", Category: "Session"},
	{Name: "/debug", Desc: "show config and state paths", Category: "Help"},
	{Name: "/theme", Desc: "switch theme (dark/light/mono)", Category: "Help"},
	{Name: "/reasoning", Desc: "set reasoning effort (low|medium|high|xhigh)", Category: "Help"},
	{Name: "/diff-base", Desc: "diff against base branch (e.g. main)", Category: "Build"},
	{Name: "/rename", Desc: "rename the active session", Category: "Session"},
	{Name: "/paste", Desc: "paste-mode toggle", Category: "Help"},
	{Name: "/mcp", Desc: "manage MCP servers (placeholder)", Category: "Help"},
	{Name: "/agents", Desc: "spawn / manage sub-agents", Category: "Session"},
	{Name: "/skills", Desc: "manage skills (not available)", Category: "Help"},
	{Name: "/tasks", Desc: "show todo list", Category: "Session"},
	{Name: "/clear", Desc: "clear chat view", Category: "Session", Shortcut: "Ctrl+L"},
	{Name: "/new", Desc: "new session", Category: "Session", Shortcut: "Ctrl+T"},
	{Name: "/quit", Desc: "exit Astra", Category: "Help"},
}

// atCompletion candidates for the @ autocomplete. Filled by the host app.
type atCompletion struct {
	Label  string
	Insert string
	Kind   string
}

type composer struct {
	ta           textarea.Model
	filtered     []slashCmd
	sel          int
	show         bool
	atShow       bool
	atQuery      string
	atCandidates []atCompletion
	atSel        int
	focused      bool
	history      []string
	histIdx      int
	plain        bool

	// Reverse-i-search (Ctrl+R) state.
	search      bool
	searchQuery string
	searchHits  []int // indexes into history
	searchPos   int   // position within searchHits

	// Modes
	bashMode      bool
	bashLine      string
	savedGlyph    string // promptGlyph saved while in bash mode
	savedTaPrompt string // textarea prompt saved while in bash mode

	// Visual mode state (set by the app so View can pick the right border tint).
	planMode bool

	// Configurable Codex key chords (set from engine keymap at startup).
	historySearchKey string
	newlineKey       string

	// promptGlyph is the composer prompt character ("›" normally, "»" while
	// an Ultra-level reasoning effort is active, mirroring Codex's
	// effort_ignition prompt accent).
	promptGlyph string

	// Files context for @ autocomplete
	fileCandidates []atCompletion

	width int

	// images holds attached local image paths rendered as Codex-style
	// "• image #N path" rows above the composer.
	images []string
}

// maxComposerHeight caps how tall the composer may grow while typing so it
// never takes over the screen (Codex caps its composer the same way).
const maxComposerHeight = 8

func newComposer(width int) composer {
	ta := textarea.New()
	ta.Placeholder = "Ask Astra to do anything"
	ta.Prompt = "› "
	ta.ShowLineNumbers = false
	ta.CharLimit = 0
	ta.SetWidth(width - 6)
	ta.SetHeight(1)
	ta.Focus()
	return composer{
		ta: ta, focused: true, width: width,
		historySearchKey: "ctrl+r",
		newlineKey:       "ctrl+j",
		promptGlyph:      "›",
	}
}

// SetPromptGlyph updates the composer prompt character (e.g. "»" for
// Ultra-level reasoning effort) on the textarea and the shimmer placeholder.
func (c *composer) SetPromptGlyph(g string) {
	if g == "" {
		g = "›"
	}
	c.promptGlyph = g
	c.ta.Prompt = g + " "
}

// SetPlanMode mirrors engine.Perm.IsPlanMode() into the composer so View can
// switch to a magenta-bordered style.
func (c *composer) SetPlanMode(on bool) {
	c.planMode = on
}

// SetWidth updates the rendered width.
func (c *composer) SetWidth(w int) {
	c.width = w
	c.ta.SetWidth(w - 6)
}

func (c *composer) AddImage(path string) {
	c.images = append(c.images, path)
}

func (c *composer) Images() []string {
	return c.images
}

func (c *composer) ClearImages() {
	c.images = nil
}

func (c *composer) Value() string { return c.ta.Value() }

func (c *composer) SetValue(v string) {
	c.ta.SetValue(v)
	c.ta.CursorEnd()
}

func (c *composer) Focus() {
	c.focused = true
	c.ta.Focus()
}

func (c *composer) Blur() {
	c.focused = false
	c.ta.Blur()
}

func (c *composer) IsBash() bool { return c.bashMode }

// EnterBash starts bash mode (used by `!` keystroke from app).
func (c *composer) EnterBash() {
	if c.bashMode {
		return
	}
	c.savedGlyph = c.promptGlyph
	c.savedTaPrompt = c.ta.Prompt
	c.bashMode = true
	c.bashLine = ""
	c.ta.SetValue("")
	c.ta.Placeholder = "" // the "Ask Astra…" placeholder must not leak into shell mode
	c.plain = true
	c.ta.Prompt = "! "
	c.promptGlyph = "!"
}

// ExitBash exits bash mode back to normal composer.
func (c *composer) ExitBash() {
	if !c.bashMode {
		return
	}
	c.bashMode = false
	c.plain = false
	c.ta.SetValue(c.bashLine)
	c.ta.Focus()
	c.promptGlyph = c.savedGlyph
	c.ta.Prompt = c.savedTaPrompt
}

// SetFileCandidates pre-populates @ file completions.
func (c *composer) SetFileCandidates(files []atCompletion) {
	c.fileCandidates = files
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

	// Bash mode intercepts everything.
	if c.bashMode {
		switch s {
		case "esc":
			c.ExitBash()
			return "", false, true
		case "enter":
			val := strings.TrimSpace(c.bashLine)
			c.bashLine = ""
			c.ExitBash()
			if val != "" {
				return val, true, true
			}
			return "", false, true
		case "backspace":
			if len(c.bashLine) > 0 {
				c.bashLine = c.bashLine[:len(c.bashLine)-1]
				c.mirrorBashLine()
			}
			return "", false, true
		}
		if key.Type == tea.KeyRunes {
			c.bashLine += key.String()
			c.mirrorBashLine()
			return "", false, true
		}
		return "", false, false
	}

	// Ctrl+R — enter (or advance) reverse incremental search.
	if s == c.historySearchKey || c.search {
		if s == c.historySearchKey && !c.search {
			c.search = true
			c.searchQuery = ""
			c.searchHits = nil
			c.searchPos = -1
			c.recomputeSearch()
			return "", false, true
		}
		// We're already in search mode — handle keys here.
		switch s {
		case "esc":
			c.search = false
			c.searchQuery = ""
			c.searchHits = nil
			c.searchPos = -1
			return "", false, true
		case c.historySearchKey:
			if len(c.searchHits) > 0 {
				c.searchPos = (c.searchPos + 1) % len(c.searchHits)
			}
			return "", false, true
		case "ctrl+s":
			if len(c.searchHits) > 0 {
				c.searchPos = (c.searchPos - 1 + len(c.searchHits)) % len(c.searchHits)
			}
			return "", false, true
		case "enter":
			c.commitSearch()
			return "", false, true
		case "backspace":
			if len(c.searchQuery) > 0 {
				c.searchQuery = c.searchQuery[:len(c.searchQuery)-1]
				c.recomputeSearch()
			}
			return "", false, true
		}
		if key.Type == tea.KeyRunes {
			c.searchQuery += key.String()
			c.recomputeSearch()
			return "", false, true
		}
		return "", false, true
	}

	value := c.ta.Value()
	if c.show || c.atShow {
		switch s {
		case "up":
			if c.show && len(c.filtered) > 0 {
				c.sel = (c.sel - 1 + len(c.filtered)) % len(c.filtered)
				return "", false, true
			}
			if c.atShow && len(c.atCandidates) > 0 {
				c.atSel = (c.atSel - 1 + len(c.atCandidates)) % len(c.atCandidates)
				return "", false, true
			}
		case "down":
			if c.show && len(c.filtered) > 0 {
				c.sel = (c.sel + 1) % len(c.filtered)
				return "", false, true
			}
			if c.atShow && len(c.atCandidates) > 0 {
				c.atSel = (c.atSel + 1) % len(c.atCandidates)
				return "", false, true
			}
		case "tab":
			if c.show && len(c.filtered) > 0 {
				c.complete(c.filtered[c.sel].Name)
				return "", false, true
			}
			if c.atShow && len(c.atCandidates) > 0 {
				c.completeAt(c.atCandidates[c.atSel])
				return "", false, true
			}
		case "enter", "ctrl+j":
			if c.show && len(c.filtered) > 0 {
				target := c.filtered[c.sel].Name
				if strings.TrimSpace(value) == target {
					c.show = false
					c.ta.SetValue("")
					return target, true, true
				}
				c.complete(target)
				return "", false, true
			}
			if c.atShow && len(c.atCandidates) > 0 {
				c.completeAt(c.atCandidates[c.atSel])
				return "", false, true
			}
		case "esc":
			c.show = false
			c.atShow = false
			return "", false, true
		}
	}

	switch {
	case s == "enter":
		if strings.TrimSpace(value) == "" {
			return "", false, true
		}
		c.pushHistory(value)
		out := value
		c.ta.SetValue("")
		c.show = false
		c.atShow = false
		return out, true, true
	case s == c.newlineKey:
		c.ta.InsertString("\n")
		return "", false, true
	case s == "alt+enter" || s == "alt+ctrl+j":
		c.ta.InsertString("\n")
		return "", false, true
	case s == "ctrl+up":
		if len(c.history) > 0 {
			if c.histIdx == 0 {
				c.histIdx = len(c.history)
			}
			c.histIdx--
			c.ta.SetValue(c.history[c.histIdx])
		}
		return "", false, true
	case s == "ctrl+down":
		if c.histIdx < len(c.history) {
			c.histIdx++
			if c.histIdx == len(c.history) {
				c.ta.SetValue("")
			} else {
				c.ta.SetValue(c.history[c.histIdx])
			}
		}
		return "", false, true
	case s == "esc":
		if c.show || c.atShow {
			c.show = false
			c.atShow = false
			return "", false, true
		}
	}
	c.ta, _ = c.ta.Update(msg)
	c.refresh()
	return "", false, false
}

// mirrorBashLine keeps the textarea value in sync with bashLine so the shell
// command the user is typing is actually visible in the input box.
func (c *composer) mirrorBashLine() {
	c.ta.SetValue(c.bashLine)
	c.ta.CursorEnd()
}

// ContentLines returns the number of lines the composer text occupies when
// wrapped to the textarea width, capped at maxComposerHeight. The composer
// sizes its box from this so it starts as a single line and grows as you
// type (Codex-style) instead of always being two lines tall.
func (c *composer) ContentLines() int {
	n := wrappedLineCount(c.ta.Value(), c.ta.Width())
	if n < 1 {
		n = 1
	}
	if n > maxComposerHeight {
		n = maxComposerHeight
	}
	return n
}

// BoxHeight returns the rendered height of the composer box (content lines
// plus the top/bottom borders); the app uses it to size the chat viewport.
func (c *composer) BoxHeight() int {
	if c.search {
		return 4 // status row + optional match preview + borders
	}
	return c.ContentLines() + 2
}

// wrappedLineCount mirrors the bubbles textarea's word-wrap algorithm
// (textarea.wrap) and returns how many visual lines the value occupies at the
// given wrap width. The composer box sizes itself from this count so it never
// shows an extra empty line nor clips content. Note the final boundary uses
// "strictly greater" rather than the textarea's ">=": the textarea appends a
// phantom blank line when a line exactly fills the width (a navigation
// quirk), which would otherwise show up as an extra empty row in the box.
func wrappedLineCount(value string, width int) int {
	if width < 1 {
		width = 1
	}
	if value == "" {
		return 1
	}
	// The textarea wraps each newline-separated logical line independently,
	// so the total is the sum of the per-line wrapped counts.
	total := 0
	for _, line := range strings.Split(value, "\n") {
		total += wrapOneLine(line, width)
	}
	return total
}

// wrapOneLine counts how many visual lines a single logical line (no
// newlines) occupies at the given width, mirroring bubbles textarea.wrap.
func wrapOneLine(line string, width int) int {
	runes := []rune(line)
	if len(runes) == 0 {
		return 1
	}
	rowWidths := []int{0} // rendered width of each wrapped line so far
	row := 0
	var word []rune
	spaces := 0
	for _, r := range runes {
		if unicode.IsSpace(r) {
			spaces++
		} else {
			word = append(word, r)
		}
		if spaces > 0 {
			if rowWidths[row]+uniseg.StringWidth(string(word))+spaces > width {
				row++
				rowWidths = append(rowWidths, 0)
				rowWidths[row] += uniseg.StringWidth(string(word)) + spaces
			} else {
				rowWidths[row] += uniseg.StringWidth(string(word)) + spaces
			}
			spaces = 0
			word = nil
		} else {
			lastCharLen := runewidth.RuneWidth(word[len(word)-1])
			if uniseg.StringWidth(string(word))+lastCharLen > width {
				if rowWidths[row] > 0 {
					row++
					rowWidths = append(rowWidths, 0)
				}
				rowWidths[row] += uniseg.StringWidth(string(word))
				word = nil
			}
		}
	}
	if rowWidths[row]+uniseg.StringWidth(string(word))+spaces > width {
		rowWidths = append(rowWidths, 0)
		row++
		spaces++
		rowWidths[row] += uniseg.StringWidth(string(word)) + spaces
	} else {
		rowWidths[row] += uniseg.StringWidth(string(word)) + spaces + 1
	}
	return row + 1
}

// refresh updates the popup state from the current value.
func (c *composer) refresh() {
	value := c.ta.Value()
	// Detect @ completion.
	if idx := strings.LastIndex(value, "@"); idx >= 0 {
		after := value[idx+1:]
		if !strings.ContainsAny(after, " \t\n") {
			c.atQuery = after
			c.refreshAt()
			c.show = false
			if c.plain {
				c.atShow = false
			} else {
				c.atShow = len(c.atCandidates) > 0
			}
			return
		}
	}
	c.atShow = false
	if c.plain {
		c.show = false
		return
	}
	if !strings.HasPrefix(value, "/") || strings.ContainsAny(value, " \t\n") {
		c.show = false
		return
	}
	prefix := strings.ToLower(value)
	c.filtered = c.filtered[:0]
	for _, cmd := range slashCommands {
		if strings.HasPrefix(strings.ToLower(cmd.Name), prefix) || fuzzyMatch(prefix, cmd.Name+" "+cmd.Desc) {
			c.filtered = append(c.filtered, cmd)
		}
	}
	sort.SliceStable(c.filtered, func(i, j int) bool {
		return c.filtered[i].Name < c.filtered[j].Name
	})
	c.show = len(c.filtered) > 0
	if c.sel >= len(c.filtered) {
		c.sel = 0
	}
}

func (c *composer) refreshAt() {
	q := strings.ToLower(c.atQuery)
	c.atCandidates = c.atCandidates[:0]
	for _, f := range c.fileCandidates {
		label := strings.ToLower(f.Label)
		// Cheap prefix/substring pre-filter before the expensive fuzzy match,
		// plus a hard scan cap, so typing after "@" stays responsive even in
		// very large repositories (fuzzyMatch only runs on likely candidates).
		if q == "" || strings.HasPrefix(label, q) || strings.Contains(label, q) || fuzzyMatch(q, f.Label) {
			c.atCandidates = append(c.atCandidates, f)
			if len(c.atCandidates) >= 200 {
				break
			}
		}
	}
	sort.Slice(c.atCandidates, func(i, j int) bool { return c.atCandidates[i].Label < c.atCandidates[j].Label })
	if len(c.atCandidates) > 30 {
		c.atCandidates = c.atCandidates[:30]
	}
	if c.atSel >= len(c.atCandidates) {
		c.atSel = 0
	}
}

// recomputeSearch walks the history (most-recent first) and finds lines
// containing the query. Hits are stored as indexes into c.history.
func (c *composer) recomputeSearch() {
	c.searchHits = c.searchHits[:0]
	if c.searchQuery == "" {
		c.searchPos = -1
		return
	}
	q := strings.ToLower(c.searchQuery)
	for i := len(c.history) - 1; i >= 0; i-- {
		if strings.Contains(strings.ToLower(c.history[i]), q) {
			c.searchHits = append(c.searchHits, i)
		}
	}
	if len(c.searchHits) > 0 {
		c.searchPos = 0
	} else {
		c.searchPos = -1
	}
}

// commitSearch applies the current selection and exits search mode.
func (c *composer) commitSearch() {
	if c.searchPos < 0 || c.searchPos >= len(c.searchHits) {
		c.search = false
		return
	}
	c.ta.SetValue(c.history[c.searchHits[c.searchPos]])
	c.search = false
	c.searchQuery = ""
	c.searchHits = nil
	c.searchPos = -1
	c.histIdx = len(c.history)
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

func (c *composer) completeAt(a atCompletion) {
	value := c.ta.Value()
	idx := strings.LastIndex(value, "@")
	if idx < 0 {
		return
	}
	insert := a.Insert
	if insert == "" {
		insert = a.Label
	}
	value = value[:idx] + "@" + insert + " "
	c.ta.SetValue(value)
	c.atShow = false
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
	if c.search {
		return c.viewSearch(width)
	}
	// Size the box to the wrapped content: one line while idle, growing as
	// the user types (capped) instead of a constant two-line box.
	c.ta.SetHeight(c.ContentLines())
	inner := c.ta.View()
	if len(c.images) == 0 && c.ta.Value() == "" && !c.bashMode {
		// Codex shimmers the "Ask …" placeholder while the composer is idle.
		// The textarea can't style the placeholder per character, so the empty
		// state renders manually (prompt + shimmer + no cursor), and the
		// textarea takes over as soon as the user types.
		inner = c.placeholderView()
	} else if len(c.images) > 0 {
		var img strings.Builder
		for i, p := range c.images {
			fmt.Fprintf(&img, "%s %s %s\n",
				styleBullet.Render(codexBullet),
				styleDim.Render(fmt.Sprintf("image #%d", i+1)),
				styleBody.Render(p))
		}
		inner = img.String() + inner
	}
	boxStyle := styleComposer
	if c.focused {
		boxStyle = styleComposerFocused
	}
	if c.bashMode {
		boxStyle = styleComposerBash
	}
	if c.planMode && !c.bashMode {
		boxStyle = styleComposerPlan
	}
	box := boxStyle.Width(width - 2).Render(inner)
	if !c.show && !c.atShow {
		if c.bashMode {
			return box + "\n" + styleDim.Render("  Shell mode: run raw commands. esc exits, enter executes.")
		}
		return box
	}
	var b strings.Builder
	b.WriteString(box)
	b.WriteString("\n")
	popWidth := width - 6
	if popWidth > 78 {
		popWidth = 78
	}
	if c.atShow {
		b.WriteString(styleTitle.Render(fmtCat("Files")) + " " + styleDim.Render("select a path · preview shown"))
		b.WriteString("\n")
		for i, a := range c.atCandidates {
			line := fmtCat(a.Kind) + "  " + styleBody.Render(padRight(a.Label, 32))
			if i == c.atSel {
				line = fmtCat(a.Kind) + "  " + styleTitle.Render("● "+padRight(a.Label, 32))
			}
			b.WriteString(lipgloss.NewStyle().Width(popWidth).Padding(0, 1).Render(line))
			b.WriteString("\n")
			if i > 9 {
				b.WriteString(styleDim.Render(fmt.Sprintf("  …+%d more", len(c.atCandidates)-i-1)))
				break
			}
		}
		// File preview for the highlighted candidate.
		if c.atSel < len(c.atCandidates) {
			cand := c.atCandidates[c.atSel]
			if cand.Kind == "File" {
				body := previewFile(cand.Insert, 8)
				if body != "" {
					b.WriteString(styleFaint.Render("── preview ──"))
					b.WriteString("\n")
					b.WriteString(styleDim.Render(body))
					b.WriteString("\n")
				}
			}
		}
		b.WriteString(styleDim.Render("↑↓ · ⏎ insert · esc cancel"))
	} else {
		b.WriteString(styleTitle.Render("Commands") + "  " + styleDim.Render("type to filter · ⏎ run"))
		b.WriteString("\n")
		for i, cmd := range c.filtered {
			cat := cmd.Category
			if cat == "" {
				cat = "Help"
			}
			shortcut := ""
			if cmd.Shortcut != "" {
				shortcut = styleDim.Render("  " + cmd.Shortcut)
			}
			line := fmtCat(cat) + styleBody.Render(padRight(cmd.Name, 12)) + "  " + styleDim.Render(cmd.Desc) + shortcut
			if i == c.sel {
				line = fmtCat(cat) + styleTitle.Render("● "+padRight(cmd.Name, 12)) + "  " + styleValue.Render(cmd.Desc) + shortcut
			}
			b.WriteString(lipgloss.NewStyle().Width(popWidth).Padding(0, 1).Render(line))
			b.WriteString("\n")
			if i > 8 {
				b.WriteString(styleDim.Render(fmt.Sprintf("  …+%d more", len(c.filtered)-i-1)))
				break
			}
		}
		b.WriteString(styleDim.Render("↑↓ · ⏎ run · esc close"))
	}
	return b.String()
}

// placeholderView renders the idle composer prompt + shimmering placeholder.
func (c *composer) placeholderView() string {
	var b strings.Builder
	b.WriteString(styleDim.Render(c.promptGlyph + " "))
	if motionEnabled() {
		b.WriteString(shimmerRender("Ask Astra to do anything", time.Now()))
	} else {
		b.WriteString(styleDim.Render("Ask Astra to do anything"))
	}
	return b.String()
}

// viewSearch renders the reverse-i-search status. The actual prompt content
// is hidden so the user can focus on the match. The layout matches
// codex-rs history_search.rs: a single-line status row above an inline
// preview of the current match.
func (c *composer) viewSearch(width int) string {
	var b strings.Builder
	var previewText string
	statusMsg := " searching…"
	indicator := " [no matches]"
	if c.searchQuery == "" {
		statusMsg = ""
	} else if len(c.searchHits) == 0 {
		statusMsg = "  no match"
	} else if c.searchPos >= 0 && c.searchPos < len(c.searchHits) {
		statusMsg = "  ⏎ accept · esc cancel"
		indicator = fmt.Sprintf(" [%d/%d]", c.searchPos+1, len(c.searchHits))
		previewText = c.history[c.searchHits[c.searchPos]]
		if len(previewText) > 100 {
			previewText = previewText[:97] + "…"
		}
	}
	b.WriteString(styleDim.Render("(reverse-i-search):") + " ")
	b.WriteString(styleValue.Render(c.searchQuery))
	b.WriteString(indicator)
	b.WriteString(statusMsg)
	b.WriteString("\n")
	if previewText != "" {
		b.WriteString(styleBody.Render("↳ " + previewText))
		b.WriteString("\n")
	}
	return styleComposerFocused.Width(width - 2).Render(b.String())
}
