package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/kevenhu001-cyber/astra-harness/internal/core"
	"github.com/kevenhu001-cyber/astra-harness/internal/engine"
	"github.com/kevenhu001-cyber/astra-harness/internal/llm"
)

const (
	modeChat       = "chat"
	modePermission = "permission"
	modeAsk        = "ask"
	modeBash       = "bash"
	modeBashResult = "bashresult"
)

type chatItem struct {
	kind      string // user | assistant | tool | system | error | evidence | unknown | claim | bash
	meta      string
	raw       string
	rendered  string
	status    string
	collapsed bool
	duration  time.Duration
}

type streamingMsg struct {
	raw   string
	start time.Time
}

type engineEventMsg struct {
	ev engine.Event
}

type indexDoneMsg struct {
	err error
}

type verifyDoneMsg struct {
	success bool
	output  string
}

type bashDoneMsg struct {
	cmd     string
	output  string
	success bool
	err     error
}

type app struct {
	root   string
	engine *engine.Engine
	cfg    *engine.Config
	events chan engine.Event

	width, height int
	vp            viewport.Model
	items         []*chatItem
	streaming     *streamingMsg
	composer      composer
	palette       palette
	sidebar       sidebar
	overlay       *overlay
	spinner       spinner.Model

	mode        string
	busy        bool
	busyAt      time.Time
	pendingPerm *engine.PermissionRequest
	pendingAsk  *askState
	bashOut     string
	bashErr     error
	status      string
	toast       string
	toastUntil  time.Time
	lastUsage   llm.Usage
	totalCost   float64
	totalTokens int
	quit        bool
	atBottom    bool
}

type askState struct {
	id       string
	question string
}

// NewApp builds the TUI model.
func NewApp(root string, cfg *engine.Config, eng *engine.Engine) *app {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(accentHi)
	a := &app{
		root: root, engine: eng, cfg: cfg, events: eng.Events,
		spinner: sp, mode: modeChat,
	}
	a.vp = viewport.New(80, 24)
	a.vp.Style = lipgloss.NewStyle().Padding(0, 1)
	a.composer = newComposer(80)
	a.sidebar = *newSidebar(eng)
	a.sidebar.visible = false
	a.palette = palette{}
	// Pre-populate file candidates for the @ autocomplete.
	a.refreshFileCandidates()
	a.addSystem(fmt.Sprintf("◆ Astra · %s · %s/%s · branch %s",
		filepath.Base(root), eng.ProviderID(), eng.Model, eng.Git.BranchOr("-")))
	a.addSystem(eng.Index.Stats())
	a.addWelcomeHints()
	return a
}

// Run starts the Bubble Tea program.
func Run(root string, cfg *engine.Config, eng *engine.Engine) error {
	a := NewApp(root, cfg, eng)
	p := tea.NewProgram(a, tea.WithAltScreen(), tea.WithMouseCellMotion(), tea.WithMouseAllMotion())
	if _, err := p.Run(); err != nil {
		return err
	}
	return nil
}

func (a *app) Init() tea.Cmd {
	return tea.Batch(a.spinner.Tick, a.waitEvent)
}

func (a *app) waitEvent() tea.Msg {
	return engineEventMsg{ev: <-a.events}
}

// refreshFileCandidates rebuilds the @ autocomplete list from the index.
func (a *app) refreshFileCandidates() {
	files := a.engine.Index.Files
	out := make([]atCompletion, 0, len(files))
	for path, fi := range files {
		if fi == nil {
			continue
		}
		rel := relPath(a.root, path)
		out = append(out, atCompletion{
			Label:  rel,
			Insert: rel,
			Kind:   "File",
		})
	}
	// Prepend a few sentinel hints to make symbol-level completion visible.
	a.composer.SetFileCandidates(out)
}

func (a *app) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		a.width = m.Width
		a.height = m.Height
		setMarkdownWidth(m.Width - 12)
		a.layout()
		a.refreshViewport()
		return a, nil
	case spinner.TickMsg:
		var cmd tea.Cmd
		a.spinner, cmd = a.spinner.Update(msg)
		if a.busy && a.streaming == nil {
			// update spinner bar
		}
		return a, cmd
	case engineEventMsg:
		return a, a.handleEngineEvent(m.ev)
	case indexDoneMsg:
		a.busy = false
		if m.err != nil {
			a.addError("index failed: " + m.err.Error())
		} else {
			a.addSystem("index rebuilt: " + a.engine.Index.Stats())
			a.refreshFileCandidates()
		}
		a.refreshViewport()
		return a, nil
	case verifyDoneMsg:
		a.busy = false
		a.addSystem("verify " + statusWord(m.success) + "\n" + m.output)
		a.refreshViewport()
		return a, nil
	case bashDoneMsg:
		a.mode = modeChat
		a.busy = false
		if m.err != nil {
			a.bashErr = m.err
		}
		out := strings.TrimRight(m.output, "\n")
		a.addBashResult(m.cmd, out, m.success, m.err)
		a.refreshViewport()
		return a, nil
	case paletteSubmitMsg:
		a.addSystem("palette → " + m.entry.title)
		return a, a.executeCommand(m.entry.command)
	case tea.MouseMsg:
		a.vp, _ = a.vp.Update(msg)
		return a, nil
	case tea.KeyMsg:
		return a.handleKey(m)
	}
	return a, nil
}

func (a *app) layout() {
	sideW := 0
	if a.sidebar.visible {
		sideW = 26
	}
	a.vp.Width = a.width - sideW - 2
	a.vp.Height = a.viewportHeight()
	a.composer.SetWidth(a.width - sideW)
	a.sidebar.SetSize(sideW, a.height-2)
	a.palette.SetSize(a.width, a.height)
}

// handleKey routes key events to the right handler.
func (a *app) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if a.quit {
		return a, tea.Quit
	}
	s := msg.String()

	// Palette has top priority while visible.
	if a.palette.visible {
		cmd, submit := a.palette.Update(msg)
		if submit {
			return a, cmd
		}
		return a, nil
	}

	// Overlay routing.
	if a.overlay != nil {
		closed, selected, handled := a.overlay.update(msg)
		if handled {
			if closed {
				if selected != "" {
					a.overlaySelect(selected)
				}
				a.overlay = nil
				a.refreshViewport()
			}
			return a, nil
		}
	}

	// Permission/ask modal handlers.
	switch a.mode {
	case modePermission:
		if a.pendingPerm == nil {
			a.mode = modeChat
			return a, nil
		}
		switch s {
		case "y":
			a.engine.AnswerPermission(a.pendingPerm.ID, engine.PermissionDecision{Allowed: true})
			a.mode = modeChat
			a.pendingPerm = nil
			a.refreshViewport()
		case "a":
			a.engine.AnswerPermission(a.pendingPerm.ID, engine.PermissionDecision{Allowed: true, Always: true})
			a.mode = modeChat
			a.pendingPerm = nil
			a.refreshViewport()
		case "n", "esc":
			a.engine.AnswerPermission(a.pendingPerm.ID, engine.PermissionDecision{Allowed: false})
			a.mode = modeChat
			a.pendingPerm = nil
			a.refreshViewport()
		case "N":
			a.engine.AnswerPermission(a.pendingPerm.ID, engine.PermissionDecision{Allowed: false, Always: true})
			a.mode = modeChat
			a.pendingPerm = nil
			a.refreshViewport()
		}
		return a, nil

	case modeAsk:
		if a.pendingAsk == nil {
			a.mode = modeChat
			return a, nil
		}
		switch s {
		case "esc":
			a.engine.AnswerAsk(a.pendingAsk.id, "[cancelled by user]")
			a.mode = modeChat
			a.pendingAsk = nil
			a.composer.plain = false
			a.composer.SetValue("")
			a.composer.Focus()
			a.refreshViewport()
			return a, nil
		}
		text, submit, _ := a.composer.update(msg)
		if submit {
			a.engine.AnswerAsk(a.pendingAsk.id, text)
			a.mode = modeChat
			a.pendingAsk = nil
			a.composer.plain = false
			a.composer.SetValue("")
			a.composer.Focus()
			a.refreshViewport()
			return a, nil
		}
		return a, nil
	}

	// Sidebar input mode: arrows + enter on the sidebar.
	if a.sidebar.visible && (s == "j" || s == "k" || s == "up" || s == "down" || s == "tab" || s == "m" || s == "left" || s == "right") {
		a.sidebar.Update(msg)
		// Only consume keys when sidebar is "claiming" the input.
		// Convention: when the composer is empty, sidebar keeps focus.
		val := strings.TrimSpace(a.composer.Value())
		if val == "" {
			if s == "enter" {
				it, ok := a.sidebar.Hit()
				if ok && it != nil {
					a.sidebarSelect(it)
				}
				return a, nil
			}
			a.refreshViewport()
			return a, nil
		}
	}

	// Global shortcuts.
	switch s {
	case "ctrl+c":
		if a.busy {
			a.engine.Stop()
			a.status = "stopping agent..."
			a.toastUntil = time.Now().Add(2 * time.Second)
			return a, nil
		}
		a.quit = true
		return a, tea.Quit
	case "ctrl+d":
		if !a.busy {
			a.quit = true
			return a, tea.Quit
		}
	case "ctrl+l":
		a.items = nil
		a.streaming = nil
		a.addSystem("chat cleared — durable state kept in .astra/")
		a.refreshViewport()
		return a, nil
	case "ctrl+b":
		a.sidebar.Toggle()
		a.layout()
		a.refreshViewport()
		return a, nil
	case "ctrl+k":
		a.palette.Show()
		return a, nil
	case "ctrl+t":
		a.executeCommand("/new")
		return a, nil
	case "ctrl+u":
		a.vp.LineUp(10)
		return a, nil
	case "ctrl+down":
		a.vp.LineDown(10)
		return a, nil
	case "?":
		if a.composer.Value() == "" {
			a.overlay = overlayHelp()
			return a, nil
		}
	case "f1":
		a.overlay = overlayHelp()
		return a, nil
	}

	// Slash command / palette quick-toggle when composer empty.
	if a.composer.Value() == "" && (s == "x" || s == "ctrl+o") {
		// Toggle collapse on the most recent tool result.
		for i := len(a.items) - 1; i >= 0; i-- {
			if a.items[i].kind == "tool" {
				a.items[i].collapsed = !a.items[i].collapsed
				break
			}
		}
		a.refreshViewport()
		return a, nil
	}

	// `!` enters bash mode (only when composer empty).
	if s == "!" && a.composer.Value() == "" && !a.composer.IsBash() {
		a.composer.EnterBash()
		a.refreshViewport()
		return a, nil
	}

	// Composer.
	text, submit, _ := a.composer.update(msg)
	if submit {
		trimmed := strings.TrimSpace(text)
		if strings.HasPrefix(trimmed, "/") {
			a.executeCommand(trimmed)
			return a, nil
		}
		if strings.HasPrefix(trimmed, "!") {
			cmd := strings.TrimSpace(strings.TrimPrefix(trimmed, "!"))
			return a, a.runBash(cmd)
		}
		return a, a.startAgent(trimmed)
	}
	a.refreshViewport()
	return a, nil
}

// sidebarSelect handles clicks/enter on the sidebar.
func (a *app) sidebarSelect(it *sidebarItem) {
	switch it.mode {
	case sidebarSessions:
		if it.id == "__new__" {
			a.executeCommand("/new")
			return
		}
		a.resumeSession(it.id)
	case sidebarFiles:
		a.overlay = overlayFile(a.engine, it.id)
	case sidebarGoals:
		if strings.HasPrefix(it.id, "clm_") {
			a.overlay = overlayClaims(a.engine.Store.State)
			a.overlay.title = "Claim: " + it.id
		} else if strings.HasPrefix(it.id, "unk_") {
			a.overlay = overlayUnknowns(a.engine.Store.State)
			a.overlay.title = "Unknown: " + it.id
		} else {
			a.overlay = overlayGoal(a.engine)
		}
	case sidebarActivity:
		a.overlay = overlayEvidence(a.engine.Store.State)
	}
}

// runBash executes a bash-mode command.
func (a *app) runBash(cmd string) tea.Cmd {
	a.bashOut = ""
	a.bashErr = nil
	a.busy = true
	a.status = "shell: " + cmd
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		out, err := runShellLocal(a.root, cmd, ctx)
		return bashDoneMsg{cmd: cmd, output: out, success: err == nil, err: err}
	}
}

func runShellLocal(root, command string, ctx context.Context) (string, error) {
	cmd := newShellCommand(ctx, root, command)
	output, err := cmd.CombinedOutput()
	return string(output), err
}

func (a *app) startAgent(prompt string) tea.Cmd {
	a.items = append(a.items, &chatItem{kind: "user", raw: prompt, rendered: a.renderUser(prompt)})
	a.busy = true
	a.busyAt = time.Now()
	a.status = "starting agent..."
	a.streaming = nil
	a.refreshViewport()
	return func() tea.Msg {
		_ = a.engine.Run(context.Background(), prompt)
		return nil
	}
}

// executeCommand runs a slash command directly.
func (a *app) executeCommand(cmdline string) tea.Cmd {
	parts := strings.Fields(cmdline)
	cmd := parts[0]
	args := ""
	if len(parts) > 1 {
		args = strings.TrimSpace(strings.TrimPrefix(cmdline, cmd))
	}
	a.addSystem("$ " + cmdline)
	switch cmd {
	case "/help":
		a.overlay = overlayHelp()
	case "/status":
		a.overlay = overlayStatus(a.engine)
	case "/claims":
		a.overlay = overlayClaims(a.engine.Store.State)
	case "/unknowns":
		a.overlay = overlayUnknowns(a.engine.Store.State)
	case "/evidence":
		a.overlay = overlayEvidence(a.engine.Store.State)
	case "/actions":
		a.overlay = overlayActions(a.engine.Store.State)
	case "/events":
		a.overlay = overlayEvents(a.engine)
	case "/tree":
		a.overlay = overlayTree(a.engine)
	case "/goal":
		if args != "" {
			desc, criteria := parseGoalArgs(args)
			g := a.engine.SetGoal(desc, criteria)
			a.addSystem(fmt.Sprintf("goal set: %s (criteria: %d)", g.Description, len(g.AcceptanceCriteria)))
		} else {
			a.overlay = overlayGoal(a.engine)
		}
	case "/model":
		if args != "" {
			if err := a.engine.SwitchModel("", args); err != nil {
				a.addError(err.Error())
			} else {
				a.addSystem("model: " + a.engine.Model)
			}
		} else {
			a.overlay = overlayModels(a.engine)
		}
	case "/provider":
		if args != "" {
			if err := a.engine.SwitchModel(args, ""); err != nil {
				a.addError(err.Error())
			} else {
				a.addSystem("provider: " + a.engine.ProviderID())
			}
		} else {
			a.overlay = overlayModels(a.engine)
		}
	case "/permissions", "/perm":
		if args != "" {
			a.engine.Perm.SetMode(args)
			a.addSystem("permission mode: " + args)
		} else {
			a.overlay = &overlay{title: "Permissions",
				body: fmt.Sprintf("current mode: %s\n\n  ask    — prompt each time\n  allow  — auto-approve\n  deny   — block writes / execution", a.engine.Perm.GetMode())}
		}
	case "/plan":
		on := !a.engine.Perm.IsPlanMode()
		a.engine.Perm.SetPlanMode(on)
		a.addSystem(fmt.Sprintf("plan mode: %v (write/execute blocked)", on))
	case "/init", "/index":
		a.addSystem("indexing repository...")
		a.busy = true
		return func() tea.Msg { return indexDoneMsg{err: a.engine.RebuildIndex()} }
	case "/verify":
		a.addSystem("verifying (tests/build)...")
		a.busy = true
		return func() tea.Msg {
			res := a.engine.Verify(context.Background())
			return verifyDoneMsg{success: res.Success, output: res.Output}
		}
	case "/commit":
		if args == "" {
			a.toast = "/commit requires a message"
			a.toastUntil = time.Now().Add(3 * time.Second)
		} else {
			out, err := a.engine.GitCommit(args)
			if err != nil {
				a.addError("commit failed: " + err.Error())
			} else {
				a.addSystem("committed: " + out)
			}
		}
	case "/branch":
		if args == "" {
			cur := a.engine.Git.BranchOr("(detached)")
			a.overlay = &overlay{title: "Branch", body: "current: " + cur + "\n\n  /branch <name>  switch\n  /branch new <name>  create + switch"}
		} else {
			if err := a.engine.GitBranch(args); err != nil {
				a.addError(err.Error())
			} else {
				a.addSystem("branch: " + args)
			}
		}
	case "/undo":
		out := a.engine.UndoLastTurn()
		a.addSystem(out)
	case "/compact":
		summary := a.engine.Compact()
		a.addSystem("context compacted: " + summary)
	case "/diff":
		a.overlay = overlayDiff(a.engine.Git)
	case "/sessions":
		a.overlay = overlaySessions(a.engine)
	case "/resume":
		if args != "" {
			a.resumeSession(args)
		} else {
			a.overlay = overlaySessions(a.engine)
		}
	case "/export":
		path := a.exportTranscript()
		a.addSystem("exported transcript: " + path)
	case "/debug":
		a.overlay = overlayDebug(a.engine)
	case "/cost":
		a.overlay = overlayCost(a.engine)
	case "/theme":
		a.overlay = overlayThemes()
	case "/paste":
		a.addSystem("paste mode is on — multi-line entry captures until you press esc twice")
		a.composer.plain = true
	case "/add-file":
		if args == "" {
			a.toast = "usage: /add-file <path>  (or type @ to autocomplete)"
			a.toastUntil = time.Now().Add(3 * time.Second)
		} else {
			full, err := a.engine.Perm.SafePath(args)
			if err != nil {
				a.addError(err.Error())
				break
			}
			data, err := readFileLimit(full, 32*1024)
			if err != nil {
				a.addError(err.Error())
				break
			}
			a.overlay = &overlay{title: "File: " + args, body: renderFilePreview(string(data), args)}
		}
	case "/mcp":
		a.overlay = overlayMcp()
	case "/agents":
		a.overlay = overlayAgents(a.engine)
	case "/tasks":
		a.overlay = overlayTasks(a.engine)
	case "/clear":
		a.items = nil
		a.streaming = nil
		a.addSystem("chat cleared — durable state kept in .astra/")
	case "/new":
		a.items = nil
		a.streaming = nil
		_ = a.engine.NewSession()
		a.addSystem("new session — state and sessions remain in .astra/")
	case "/quit", "/exit", "/q":
		a.quit = true
		return tea.Quit
	default:
		a.addError("unknown command: " + cmd + " (try /help or ⌘K)")
	}
	a.refreshViewport()
	return nil
}

func parseGoalArgs(args string) (string, []string) {
	// Criteria begin after a " -- " or " ; " separator.
	for _, sep := range []string{" ; ", " -- ", " | "} {
		if idx := strings.Index(args, sep); idx >= 0 {
			desc := strings.TrimSpace(args[:idx])
			rest := strings.TrimSpace(args[idx+len(sep):])
			criteria := splitCriteria(rest)
			return desc, criteria
		}
	}
	return args, nil
}

func splitCriteria(rest string) []string {
	parts := []string{}
	for _, p := range strings.Split(rest, ";") {
		t := strings.TrimSpace(p)
		if t != "" {
			parts = append(parts, t)
		}
	}
	if len(parts) == 0 {
		parts = append(parts, strings.TrimSpace(rest))
	}
	return parts
}

// overlaySelect handles the action when an overlay item is confirmed.
func (a *app) overlaySelect(selected string) {
	switch a.overlay.title {
	case "Sessions":
		a.resumeSession(selected)
	case "Models — pick provider/model":
		parts := strings.SplitN(selected, "|", 2)
		if len(parts) == 2 {
			if err := a.engine.SwitchModel(parts[0], parts[1]); err != nil {
				a.addError(err.Error())
			} else {
				a.addSystem(fmt.Sprintf("switched to %s/%s", parts[0], parts[1]))
			}
		}
	}
}

func (a *app) resumeSession(id string) {
	sess, err := a.engine.Store.LoadSession(id)
	if err != nil {
		a.addError("load session: " + err.Error())
		return
	}
	if err := a.engine.LoadSession(sess); err != nil {
		a.addError("resume session: " + err.Error())
		return
	}
	a.items = nil
	a.streaming = nil
	a.addSystem(fmt.Sprintf("resumed session %s (%d messages)", sess.ID, len(sess.Messages)))
}

func (a *app) exportTranscript() string {
	dir := filepath.Join(a.root, ".astra", "exports")
	_ = os.MkdirAll(dir, 0o755)
	path := filepath.Join(dir, "transcript-"+time.Now().Format("20060102-150405")+".md")
	var b strings.Builder
	b.WriteString("# Astra transcript\n\n")
	for _, it := range a.items {
		switch it.kind {
		case "user":
			fmt.Fprintf(&b, "## You\n\n%s\n\n", it.raw)
		case "assistant":
			fmt.Fprintf(&b, "## Astra\n\n%s\n\n", it.raw)
		case "tool":
			fmt.Fprintf(&b, "### Tool: %s (%s)\n\n```\n%s\n```\n\n", it.meta, it.status, it.raw)
		case "system":
			fmt.Fprintf(&b, "_%s_\n\n", it.raw)
		case "bash":
			fmt.Fprintf(&b, "### $ %s\n\n```\n%s\n```\n\n", it.meta, it.raw)
		case "evidence", "unknown", "claim":
			fmt.Fprintf(&b, "_%s_: %s\n\n", it.kind, it.raw)
		}
	}
	_ = os.WriteFile(path, []byte(b.String()), 0o644)
	return path
}

// Engine event handling ------------------------------------------------------

func (a *app) handleEngineEvent(ev engine.Event) tea.Cmd {
	switch ev.Type {
	case engine.EvDelta:
		if a.streaming == nil {
			a.streaming = &streamingMsg{start: time.Now()}
		}
		if text, ok := ev.Data.(string); ok {
			a.streaming.raw += text
		}
	case engine.EvAssistantStart:
		a.streaming = &streamingMsg{start: time.Now()}
		a.status = "agent thinking..."
	case engine.EvAssistantEnd:
		if a.streaming != nil {
			data, _ := ev.Data.(map[string]any)
			content, _ := data["content"].(string)
			dur := time.Since(a.streaming.start)
			a.items = append(a.items, &chatItem{
				kind: "assistant", meta: a.engine.Model, raw: content,
				rendered: a.renderAssistant(content, dur),
				duration: dur,
			})
			a.streaming = nil
		}
		a.status = "ready"
	case engine.EvToolStart:
		data, _ := ev.Data.(map[string]any)
		name, _ := data["name"].(string)
		args, _ := data["arguments"].(string)
		it := &chatItem{kind: "tool", meta: name, status: "running", raw: args}
		a.items = append(a.items, it)
		a.status = "tool: " + name
	case engine.EvToolEnd:
		data, _ := ev.Data.(map[string]any)
		name, _ := data["name"].(string)
		success, _ := data["success"].(bool)
		output, _ := data["output"].(string)
		// Find the most recent tool-start and update it.
		for i := len(a.items) - 1; i >= 0; i-- {
			if a.items[i].kind == "tool" && a.items[i].meta == name && a.items[i].status == "running" {
				a.items[i].status = statusWord(success)
				a.items[i].raw = output
				a.items[i].rendered = a.renderTool(name, success, output)
				break
			}
		}
		a.status = "ready"
	case engine.EvPermission:
		if req, ok := ev.Data.(engine.PermissionRequest); ok {
			a.pendingPerm = &req
			a.mode = modePermission
			a.composer.Blur()
		}
	case engine.EvAskUser:
		if data, ok := ev.Data.(map[string]any); ok {
			id, _ := data["id"].(string)
			q, _ := data["question"].(string)
			a.pendingAsk = &askState{id: id, question: q}
			a.mode = modeAsk
			a.composer.plain = true
			a.composer.SetValue("")
			a.composer.Focus()
		}
	case engine.EvEvidence:
		a.addChip("evidence", fmt.Sprintf("+%s · %s", evidenceShort(ev.Data), time.Now().Format("15:04:05")))
	case engine.EvUnknown:
		a.addChip("unknown", fmt.Sprintf("p=%.2f · %s", unknownPriority(ev.Data), unknownDesc(ev.Data)))
	case engine.EvClaim:
		a.addChip("claim", claimText(ev.Data))
	case engine.EvSystem:
		if s, ok := ev.Data.(string); ok {
			a.addSystem(s)
		}
	case engine.EvError:
		if s, ok := ev.Data.(string); ok {
			a.addError(s)
		}
	case engine.EvStatus:
		if s, ok := ev.Data.(string); ok {
			a.status = s
		}
	case engine.EvUsage:
		if u, ok := ev.Data.(llm.Usage); ok {
			a.lastUsage = u
			a.totalTokens += u.InputTokens + u.OutputTokens
			a.totalCost += approximateCost(a.engine.Model, u)
		}
	case engine.EvDone:
		a.busy = false
		a.busyAt = time.Time{}
		a.status = "ready"
		if a.streaming != nil {
			a.streaming = nil
		}
		if data, ok := ev.Data.(map[string]any); ok {
			if errStr, _ := data["error"].(string); errStr != "" {
				a.addError("run failed: " + errStr)
			}
		}
		a.addSystem("agent finished — state saved to .astra/")
	case engine.EvAction:
		if v, ok := ev.Data.(*core.Action); ok && v != nil {
			a.addSystem(fmt.Sprintf("→ %s · %s (u=%.2f)", v.Type, v.Description, v.Utility))
		}
	}
	a.refreshViewport()
	return nil
}

// View -----------------------------------------------------------------------

func (a *app) View() string {
	if a.quit {
		return ""
	}
	sideW := 0
	if a.sidebar.visible {
		sideW = 26
	}
	header := a.renderHeader()
	var main string
	switch {
	case a.palette.visible:
		main = a.palette.View()
	case a.overlay != nil:
		main = a.overlay.View(a.width-sideW, a.viewportHeight())
	default:
		main = a.vp.View()
	}
	footer := a.renderStatusBar()

	var composerView string
	switch a.mode {
	case modePermission:
		composerView = a.renderPermission()
	case modeAsk:
		composerView = a.renderAsk()
	default:
		composerView = a.composer.View(a.width - sideW)
	}

	layout := header + "\n" + main + "\n" + composerView + "\n" + footer
	if a.sidebar.visible {
		side := a.sidebar.View()
		return lipgloss.JoinHorizontal(lipgloss.Top, layout, side)
	}
	return layout
}

func (a *app) renderHeader() string {
	var b strings.Builder
	b.WriteString(styleBrand.Render("◆ Astra"))
	b.WriteString("  ")
	b.WriteString(styleDim.Render("uncertainty-driven runtime"))
	if a.busy {
		b.WriteString("  ")
		b.WriteString(a.spinner.View())
		elapsed := time.Since(a.busyAt).Truncate(time.Second)
		b.WriteString(styleDim.Render(fmt.Sprintf(" %s", elapsed)))
	}
	b.WriteString("  ")
	b.WriteString(styleSubtle.Render(fmt.Sprintf("%s/%s", a.engine.ProviderID(), a.engine.Model)))
	mode := "ask"
	if a.engine.Perm.IsPlanMode() {
		mode = "plan"
	} else if a.engine.Perm.GetMode() == engine.ModeAllow {
		mode = "allow"
	} else if a.engine.Perm.GetMode() == engine.ModeDeny {
		mode = "deny"
	}
	b.WriteString("  ")
	b.WriteString(styleDim.Render("["+mode+"]"))
	if br := a.engine.Git.BranchOr(""); br != "" {
		b.WriteString("  ")
		b.WriteString(styleDim.Render("⎇ "+br))
	}
	if g := a.engine.Store.ActiveGoal(); g != nil {
		b.WriteString("  ")
		b.WriteString(styleDim.Render("◆"))
		b.WriteString(styleValue.Render(fmt.Sprintf(" %.0f%% ", g.Progress*100)))
	}
	return styleHeaderRow.Render(b.String())
}

// widthAvail returns the available horizontal space in the main pane,
// subtracting the visible sidebar width when the sidebar is open.
func (a *app) widthAvail() int {
	w := a.width
	if a.sidebar.visible {
		w -= 26
	}
	if w < 40 {
		return 40
	}
	return w
}

func (a *app) renderStatusBar() string {
	left := a.status
	if left == "" {
		left = "ready — describe a task or type /help · ⌘K palette · ! shell · @ files"
	}
	if time.Now().Before(a.toastUntil) {
		left = a.toast
	}
	rightParts := []string{
		fmt.Sprintf("⚑ claims %d", len(a.engine.Store.State.Claims)),
		fmt.Sprintf("? unknowns %d", len(a.engine.Store.State.Unknowns)),
		fmt.Sprintf("▣ evidence %d", len(a.engine.Store.State.Evidence)),
	}
	if a.lastUsage.InputTokens > 0 || a.lastUsage.OutputTokens > 0 {
		rightParts = append(rightParts, fmt.Sprintf("↻ %d→%d tok", a.lastUsage.InputTokens, a.lastUsage.OutputTokens))
		rightParts = append(rightParts, fmt.Sprintf("$ $%.4f", a.totalCost))
	}
	right := strings.Join(rightParts, "  ")
	widthAvail := a.widthAvail() - 2
	leftW := lipgloss.Width(left)
	rightW := lipgloss.Width(right)
	pad := widthAvail - leftW - rightW - 1
	if pad < 1 {
		pad = 1
	}
	// If even pad=1 overflows (e.g. ultra-narrow terminal), trim `left` to fit.
	if leftW+rightW+1 > widthAvail {
		maxLeft := widthAvail - rightW - 1
		if maxLeft < 4 {
			maxLeft = 4
		}
		if leftW > maxLeft {
			left = truncate(left, maxLeft)
			leftW = lipgloss.Width(left)
			pad = widthAvail - leftW - rightW - 1
		}
	}
	line := left + strings.Repeat(" ", pad) + right
	return styleStatusBar.Width(a.widthAvail()).Render(line)
}

func (a *app) renderPermission() string {
	if a.pendingPerm == nil {
		return ""
	}
	req := a.pendingPerm
	var b strings.Builder
	b.WriteString(styleTitle.Render("🔒 Permission required · "+req.Kind) + "\n")
	b.WriteString("target:     " + req.Target + "\n")
	if req.Command != "" {
		b.WriteString("command:    " + req.Command + "\n")
	}
	if req.Description != "" {
		b.WriteString("description:" + req.Description + "\n")
	}
	b.WriteString(styleDim.Render(req.Risk) + "\n")
	b.WriteString(styleKey.Render("y") + " allow · " + styleKey.Render("a") + " always · " + styleKey.Render("n") + " deny · " + styleKey.Render("N") + " always deny · " + styleKey.Render("esc") + " deny once")
	return styleComposer.Width(a.width - 2).Render(b.String())
}

func (a *app) renderAsk() string {
	if a.pendingAsk == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString(styleTitle.Render("❓ Question from agent") + "\n")
	b.WriteString(a.pendingAsk.question + "\n")
	b.WriteString(styleDim.Render("type an answer and press enter · esc to cancel") + "\n")
	return styleComposer.Width(a.width-2).Render(b.String()) + "\n" + a.composer.View(a.width)
}

func (a *app) viewportHeight() int {
	h := a.height - 6
	if h < 5 {
		return 5
	}
	return h
}

func (a *app) refreshViewport() {
	a.atBottom = a.vp.ScrollPercent() > 0.97
	var parts []string
	for _, it := range a.items {
		if it.rendered != "" {
			parts = append(parts, it.rendered)
		} else {
			parts = append(parts, a.renderItem(it))
		}
	}
	if a.streaming != nil {
		parts = append(parts, a.renderAssistant(a.streaming.raw, time.Since(a.streaming.start)))
	}
	a.vp.SetContent(strings.Join(parts, "\n"))
	if a.atBottom {
		a.vp.GotoBottom()
	}
}

func (a *app) renderItem(it *chatItem) string {
	if it.rendered != "" {
		return it.rendered
	}
	switch it.kind {
	case "user":
		return a.renderUser(it.raw)
	case "tool":
		return a.renderTool(it.meta, it.status == "SUCCEEDED", it.raw)
	case "system":
		return styleSystem.Render("· " + it.raw)
	case "error":
		return styleError.Render("✗ " + it.raw)
	case "evidence", "unknown", "claim":
		return chip(it.kind, it.kind) + " " + styleDim.Render(it.raw)
	case "bash":
		return styleToolBox.Width(a.width - 4).Render(
			styleKey.Render("$")+" "+styleTitle.Render(it.meta) + "\n\n" +
				renderDiff(it.raw, ""))
	default:
		return it.raw
	}
}

func (a *app) renderUser(text string) string {
	inner := styleBody.Width(max(30, a.width-12)).Render(text)
	return styleUserBox.Width(a.width - 4).Render(
		chip("user", "You") + "\n\n" + inner)
}

func (a *app) renderAssistant(md string, dur time.Duration) string {
	body := renderMarkdown(md)
	headerRight := styleDim.Render(fmt.Sprintf("· %s · %s", a.engine.Model, dur.Truncate(time.Millisecond)))
	title := styleTitle.Render("◆ Astra") + "  " + headerRight
	if body == "" {
		body = styleDim.Render("…")
	}
	return styleAssistantBox.Width(a.width - 4).Render(title + "\n\n" + body)
}

func (a *app) renderTool(name string, success bool, output string) string {
	statusColor := styleError
	statusText := "FAILED"
	boxStyle := styleToolBoxErr
	if success {
		statusColor = styleOk
		statusText = "OK"
		boxStyle = styleToolBoxOK
	} else {
		boxStyle = styleToolBox
	}
	var body string
	// Detect if this is a structured tool output (diff) or plain output.
	if fname, diff, ok := detectDiff(output); ok {
		body = renderDiff(diff, fname)
	} else if name == "read" || name == "edit_file" || name == "write_file" {
		body = output
	} else {
		body = output
	}
	if a.findLastToolItem().collapsed {
		body = styleDim.Render("(collapsed — press x or ⌘O to expand)")
	} else if len(body) > 4000 {
		body = body[:4000] + "\n…" + styleDim.Render(fmt.Sprintf(" (+%d chars — x to collapse)", len(body)-4000))
	}
	title := styleKey.Render("⌘ "+name) + " " + statusColor.Render(statusText)
	return boxStyle.Width(a.width - 4).Render(title + "\n\n" + lipgloss.NewStyle().Width(a.width-8).Render(body))
}

func (a *app) findLastToolItem() *chatItem {
	for i := len(a.items) - 1; i >= 0; i-- {
		if a.items[i].kind == "tool" {
			return a.items[i]
		}
	}
	return nil
}

func (a *app) addSystem(text string) {
	a.items = append(a.items, &chatItem{kind: "system", raw: text})
	a.refreshViewport()
}

func (a *app) addError(text string) {
	a.items = append(a.items, &chatItem{kind: "error", raw: text})
	a.refreshViewport()
}

func (a *app) addChip(kind, text string) {
	a.items = append(a.items, &chatItem{kind: kind, raw: text})
	a.refreshViewport()
}

func (a *app) addBashResult(cmd, output string, ok bool, err error) {
	body := output
	if err != nil {
		body = output + "\n[exit " + err.Error() + "]"
	}
	a.items = append(a.items, &chatItem{kind: "bash", meta: cmd, raw: body, status: statusWord(ok)})
}

func (a *app) addWelcomeHints() {
	a.addSystem(styleDim.Render("Keybindings:") +
		"  enter send · alt+enter newline · ctrl+c stop · ctrl+b sidebar · ctrl+k palette · ctrl+l clear · ctrl+t new · ctrl+u/d scroll · ? help")
	a.addSystem(styleDim.Render("Composer shortcuts:") +
		"  / commands · @ files · ! shell · alt+enter newline · ↑↓ history")
}

// Helpers --------------------------------------------------------------

// statusWord is the local UI-flavored variant — the engine exposes its own
// statusWord (PASSED/FAILED) for verification output, which differs here.
func statusWord(ok bool) string {
	if ok {
		return "SUCCEEDED"
	}
	return "FAILED"
}

func evidenceShort(v any) string {
	if e, ok := v.(*core.Evidence); ok {
		return e.Kind + " · " + e.Source
	}
	return fmt.Sprint(v)
}

func unknownPriority(v any) float64 {
	if u, ok := v.(*core.Unknown); ok {
		return u.Priority
	}
	return 0
}

func unknownDesc(v any) string {
	if u, ok := v.(*core.Unknown); ok {
		return u.Description
	}
	return fmt.Sprint(v)
}

func claimText(v any) string {
	if c, ok := v.(*core.Claim); ok {
		return fmt.Sprintf("[%s] %s %s %s", c.Status, c.Subject, c.Predicate, c.Object)
	}
	return fmt.Sprint(v)
}

// relPath routes through the engine helper so the same logic applies to
// both the TUI sidebar and any engine-internal tool output.
func relPath(root, path string) string {
	return engine.RelPath(root, path)
}
