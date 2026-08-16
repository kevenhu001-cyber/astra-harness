package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	atclip "github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/kevenhu001-cyber/astra-harness/internal/auth"
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
	kind      string // user | assistant | tool | system | error | evidence | unknown | claim | bash | sep
	meta      string
	args      string // tool call arguments (JSON)
	raw       string
	rendered  string
	status    string
	collapsed bool
	duration  time.Duration
	exitCode  string
	started   time.Time
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

type externalEditDoneMsg struct {
	text string
	err  error
}

type imagePastedMsg struct {
	path string
	err  error
}

type loginDoneMsg struct {
	cred *auth.Credential
	err  error
}

type bashDoneMsg struct {
	cmd     string
	output  string
	success bool
	err     error
}

type bashLineMsg struct {
	cmd    string
	line   string
	stream string // "stdout" | "stderr"
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

	mode                string
	busy                bool
	busyAt              time.Time
	pendingPerm         *engine.PermissionRequest
	pendingAsk          *askState
	bashOut             string
	bashErr             error
	status              string
	toast               string
	toastUntil          time.Time
	lastUsage           llm.Usage
	totalCost           float64
	totalTokens         int
	turns               int
	successCnt          int
	failCnt             int
	startedAt           time.Time
	quit                bool
	quitArmed           bool
	quitArmedAt         time.Time
	escArmed            bool
	escArmedAt          time.Time
	keymapCaptureAction string
	atBottom            bool
	toolWorkThisTurn    bool
	userEmail           string
	deviceFlow          *auth.DeviceFlow
	loginOverlay        *loginOverlay
}

type askState struct {
	id       string
	question string
}

// NewApp builds the TUI model.
func NewApp(root string, cfg *engine.Config, eng *engine.Engine) *app {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(activePalette().AccentHi)
	a := &app{
		root: root, engine: eng, cfg: cfg, events: eng.Events,
		spinner: sp, mode: modeChat,
		startedAt: time.Now(),
	}
	if cred, err := auth.LoadCredential(); err == nil && cred != nil {
		a.userEmail = cred.User.Email
	}
	a.vp = viewport.New(80, 24)
	// Codex renders history edge-to-edge: gutters are part of each cell
	// (e.g. "• ", "› ", "  └ "), so the viewport must not add its own
	// horizontal padding or long cells wrap one column early.
	a.vp.Style = lipgloss.NewStyle()
	a.composer = newComposer(80)
	a.composer.historySearchKey = eng.KeymapBinding("history_search")
	a.composer.newlineKey = eng.KeymapBinding("newline")
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
	return RunWithOptions(root, cfg, eng, false)
}

// RunWithOptions starts the Bubble Tea program with optional Codex-style
// resume picker when saved sessions exist.
func RunWithOptions(root string, cfg *engine.Config, eng *engine.Engine, showResumePicker bool) error {
	a := NewApp(root, cfg, eng)
	if showResumePicker {
		if sessions, err := eng.Store.ListSessions(); err == nil && len(sessions) > 0 {
			a.overlay = overlaySessions(eng)
		}
	}
	p := tea.NewProgram(a, tea.WithAltScreen(), tea.WithMouseCellMotion(), tea.WithMouseAllMotion())
	if _, err := p.Run(); err != nil {
		return err
	}
	return nil
}

func (a *app) Init() tea.Cmd {
	return tea.Batch(a.spinner.Tick, a.waitEvent, tea.SetWindowTitle("Astra · "+filepath.Base(a.root)))
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
	case externalEditDoneMsg:
		if m.err != nil {
			a.addError("external editor: " + m.err.Error())
		} else {
			a.composer.SetValue(m.text)
			a.composer.Focus()
			a.addSystem("external edit applied")
		}
		a.busy = false
		a.refreshViewport()
		return a, nil
	case imagePastedMsg:
		a.busy = false
		if m.err != nil {
			a.addError("paste image: " + m.err.Error())
		} else {
			a.composer.AddImage(m.path)
			a.addSystem("attached " + m.path)
		}
		a.refreshViewport()
		return a, nil
	case loginDoneMsg:
		a.deviceFlow = nil
		a.busy = false
		if m.err != nil {
			a.addError("login: " + m.err.Error())
		} else if m.cred != nil {
			if err := auth.SaveCredential(m.cred); err != nil {
				a.addError("save credential: " + err.Error())
			} else {
				a.userEmail = m.cred.User.Email
				a.addSystem("logged in as " + m.cred.User.Email)
			}
		}
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

	// Keymap capture mode: the next key becomes the binding for the selected
	// action (Esc cancels).
	if a.keymapCaptureAction != "" {
		if s == "esc" {
			a.keymapCaptureAction = ""
			a.addSystem("keymap: cancelled")
			return a, nil
		}
		action := a.keymapCaptureAction
		a.keymapCaptureAction = ""
		if err := a.engine.SetKeymap(action, s); err != nil {
			a.addError(err.Error())
		} else {
			a.addSystem("keymap: " + action + " → " + s)
		}
		return a, nil
	}

	// Dynamic Codex-style bindings (configurable via /keymap).
	if s != "" && s == a.engine.KeymapBinding("external_editor") {
		draft := a.composer.Value()
		a.busy = true
		a.addSystem("external editor: waiting...")
		return a, func() tea.Msg {
			text, err := openExternalEditor(draft)
			return externalEditDoneMsg{text: text, err: err}
		}
	}
	if s != "" && s == a.engine.KeymapBinding("transcript") {
		a.overlay = overlayTranscript(a)
		return a, nil
	}
	if s != "" && s == a.engine.KeymapBinding("model_picker") {
		a.overlay = overlayModels(a.engine)
		return a, nil
	}
	if s != "" && s == a.engine.KeymapBinding("open_help") && a.composer.Value() == "" {
		a.overlay = overlayShortcuts(a.engine)
		return a, nil
	}
	if s != "" && s == a.engine.KeymapBinding("page_up") {
		a.vp.LineUp(max(1, a.viewportHeight()/2))
		return a, nil
	}
	if s != "" && s == a.engine.KeymapBinding("page_down") {
		a.vp.LineDown(max(1, a.viewportHeight()/2))
		return a, nil
	}
	if s != "" && s == a.engine.KeymapBinding("scroll_up") {
		a.vp.LineUp(10)
		return a, nil
	}
	if s != "" && s == a.engine.KeymapBinding("scroll_down") {
		a.vp.LineDown(10)
		return a, nil
	}
	if s != "" && s == a.engine.KeymapBinding("clear") {
		a.executeCommand("/clear")
		return a, nil
	}
	if s != "" && s == a.engine.KeymapBinding("new_session") {
		a.executeCommand("/new")
		return a, nil
	}
	if s != "" && s == a.engine.KeymapBinding("palette") {
		a.palette.Show()
		return a, nil
	}
	if s != "" && s == a.engine.KeymapBinding("copy") {
		for i := len(a.items) - 1; i >= 0; i-- {
			if a.items[i].kind == "assistant" {
				if err := atclip.WriteAll(a.items[i].raw); err != nil {
					a.addError("copy: " + err.Error())
				} else {
					a.toast = "copied last response"
					a.toastUntil = time.Now().Add(2 * time.Second)
				}
				break
			}
		}
		return a, nil
	}

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
		allowKey := a.engine.KeymapBinding("permission_allow")
		alwaysKey := a.engine.KeymapBinding("permission_always")
		denyKey := a.engine.KeymapBinding("permission_deny")
		neverKey := a.engine.KeymapBinding("permission_never")
		switch {
		case s == allowKey:
			a.engine.AnswerPermission(a.pendingPerm.ID, engine.PermissionDecision{Allowed: true})
			a.mode = modeChat
			a.pendingPerm = nil
			a.refreshViewport()
		case s == alwaysKey:
			a.engine.AnswerPermission(a.pendingPerm.ID, engine.PermissionDecision{Allowed: true, Always: true})
			a.mode = modeChat
			a.pendingPerm = nil
			a.refreshViewport()
		case s == denyKey || s == "esc":
			a.engine.AnswerPermission(a.pendingPerm.ID, engine.PermissionDecision{Allowed: false})
			a.mode = modeChat
			a.pendingPerm = nil
			a.refreshViewport()
		case s == neverKey:
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
	now := time.Now()
	if a.quitArmed && now.Sub(a.quitArmedAt) > 3*time.Second {
		a.quitArmed = false
	}
	if a.escArmed && now.Sub(a.escArmedAt) > 3*time.Second {
		a.escArmed = false
	}
	switch s {
	case "ctrl+c":
		if a.busy {
			a.engine.Stop()
			a.status = "stopping agent..."
			a.toastUntil = time.Now().Add(2 * time.Second)
			return a, nil
		}
		if a.quitArmed {
			a.quit = true
			return a, tea.Quit
		}
		a.quitArmed = true
		a.quitArmedAt = now
		a.status = "ctrl+c again to quit"
		a.toastUntil = now.Add(3 * time.Second)
		return a, nil
	case "esc":
		if !a.busy && a.composer.Value() == "" {
			if a.escArmed {
				a.escArmed = false
				a.overlay = overlayBacktrack(a)
				return a, nil
			}
			a.escArmed = true
			a.escArmedAt = now
			a.status = "esc esc to edit previous message"
			a.toastUntil = now.Add(3 * time.Second)
			return a, nil
		}
	case "ctrl+d":
		if !a.busy {
			a.quit = true
			return a, tea.Quit
		}
	default:
		a.quitArmed = false
		a.escArmed = false
	}
	switch s {
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
	case "ctrl+v":
		if a.composer.Value() != "" {
			break
		}
		a.busy = true
		a.addSystem("paste image: reading clipboard...")
		return a, func() tea.Msg {
			path, err := pasteClipboardImage()
			if err == nil {
				return imagePastedMsg{path: path}
			}
			if text, terr := readClipboardText(); terr == nil && looksLikeImagePath(text) {
				return imagePastedMsg{path: text}
			}
			return imagePastedMsg{err: err}
		}
	case "ctrl+t":
		a.overlay = overlayTranscript(a)
		return a, nil
	case "ctrl+u":
		a.vp.LineUp(10)
		return a, nil
	case "ctrl+down":
		a.vp.LineDown(10)
		return a, nil
	case "?":
		if a.composer.Value() == "" {
			a.overlay = overlayShortcuts(a.engine)
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
		if strings.HasPrefix(trimmed, "/") || strings.HasPrefix(trimmed, "!") {
			a.composer.ClearImages()
		} else if len(a.composer.Images()) > 0 {
			var img strings.Builder
			for i, p := range a.composer.Images() {
				fmt.Fprintf(&img, "![Image #%d](%s)\n", i+1, p)
			}
			text = img.String() + text
			a.composer.ClearImages()
		}
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

// runBash executes a bash-mode command and streams lines into bashOut.
func (a *app) runBash(cmd string) tea.Cmd {
	a.bashOut = ""
	a.bashErr = nil
	a.busy = true
	a.status = "shell: " + cmd
	return func() tea.Msg {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		c, stdoutCh, stderrCh, err := streamShell(ctx, a.root, cmd)
		if err != nil {
			return bashDoneMsg{cmd: cmd, success: false, err: err}
		}
		var lines []string
		for stdoutCh != nil || stderrCh != nil {
			select {
			case line, ok := <-stdoutCh:
				if !ok {
					stdoutCh = nil
					continue
				}
				lines = append(lines, line)
			case line, ok := <-stderrCh:
				if !ok {
					stderrCh = nil
					continue
				}
				lines = append(lines, line)
			}
		}
		err = c.Wait()
		return bashDoneMsg{cmd: cmd, output: joinLines(lines), success: err == nil, err: err}
	}
}

func joinLines(lines []string) string {
	out := ""
	for i, l := range lines {
		if i > 0 {
			out += "\n"
		}
		out += l
	}
	return out
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
	a.toolWorkThisTurn = false
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
	display := cmdline
	if cmd == "/set-key" {
		display = "/set-key <provider> ****"
	}
	a.addSystem("$ " + display)
	switch cmd {
	case "/help":
		a.overlay = overlayHelp()
	case "/status":
		a.overlay = overlayStatus(a)
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
	case "/config":
		a.overlay = overlayProviderConfig(a.engine)
	case "/set-url":
		id, val, err := providerArg(args)
		if err != nil {
			a.addError(err.Error())
			break
		}
		if err := a.engine.UpdateProvider(id, val, "", ""); err != nil {
			a.addError(err.Error())
		} else {
			a.addSystem("provider " + id + " url: " + val)
		}
	case "/set-key":
		id, val, err := providerArg(args)
		if err != nil {
			a.addError(err.Error())
			break
		}
		if err := a.engine.UpdateProvider(id, "", val, ""); err != nil {
			a.addError(err.Error())
		} else {
			a.addSystem("provider " + id + " api key saved (config file is 0600)")
		}
	case "/set-model":
		id, val, err := providerArg(args)
		if err != nil {
			a.addError(err.Error())
			break
		}
		if err := a.engine.UpdateProvider(id, "", "", val); err != nil {
			a.addError(err.Error())
		} else {
			a.addSystem("provider " + id + " model: " + val)
		}
	case "/statusline":
		if args == "" || args == "list" {
			a.overlay = overlayStatusLine(a.engine)
		} else if args == "reset" {
			if err := a.engine.SetStatusLine(append([]string(nil), engine.DefaultStatusLine...)); err != nil {
				a.addError(err.Error())
			} else {
				a.addSystem("statusline: reset to defaults")
			}
		} else {
			items := strings.Fields(args)
			if err := a.engine.SetStatusLine(items); err != nil {
				a.addError(err.Error())
			} else {
				a.addSystem("statusline: " + strings.Join(items, " · "))
			}
		}
	case "/keymap":
		if args == "reset" {
			if err := a.engine.ResetKeymap(); err != nil {
				a.addError(err.Error())
			} else {
				a.addSystem("keymap: reset to Codex defaults")
			}
		} else {
			a.overlay = overlayKeymap(a.engine)
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
	case "/login":
		if a.deviceFlow != nil {
			a.toast = "login already in progress"
			break
		}
		if a.userEmail != "" {
			a.toast = "already signed in as " + a.userEmail
			break
		}
		server := a.cfg.AuthServer
		if server == "" {
			server = auth.DefaultServer
		}
		flow, err := auth.New(server).StartDevice(context.Background())
		if err != nil {
			a.addError("login: " + err.Error())
			break
		}
		a.deviceFlow = flow
		a.addSystem(fmt.Sprintf("device login · open %s · code %s (expires %ds)", flow.VerificationURI, flow.UserCode, flow.ExpiresIn))
		_ = auth.OpenBrowser(flow.VerificationURI)
		return func() tea.Msg {
			c := auth.New(server)
			interval := time.Duration(flow.Interval) * time.Second
			if interval < time.Second {
				interval = 5 * time.Second
			}
			deadline := time.Now().Add(time.Duration(flow.ExpiresIn) * time.Second)
			for {
				if time.Now().After(deadline) {
					return loginDoneMsg{err: fmt.Errorf("authorization expired")}
				}
				res, err := c.PollDevice(context.Background(), flow.DeviceCode)
				if err != nil {
					time.Sleep(interval)
					continue
				}
				switch res.Status {
				case "approved":
					return loginDoneMsg{cred: &auth.Credential{
						Server: server, Token: res.AccessToken, User: *res.User,
						ExpiresAt: time.Now().Add(30 * 24 * time.Hour),
					}}
				case "expired":
					return loginDoneMsg{err: fmt.Errorf("authorization expired")}
				default:
					time.Sleep(interval)
				}
			}
		}
	case "/logout":
		if err := auth.ClearCredential(); err != nil {
			a.addError("logout: " + err.Error())
		} else {
			a.userEmail = ""
			a.addSystem("signed out")
		}
	case "/whoami":
		if a.userEmail == "" {
			a.addSystem("not signed in — run /login")
		} else {
			a.addSystem("signed in as " + a.userEmail)
		}
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
	case "/diff-base":
		base := a.engine.Git.DefaultBranch()
		if args != "" {
			base = args
		}
		a.overlay = overlayDiffBase(a.engine.Git, base)
	case "/rename":
		if args == "" {
			a.toast = "usage: /rename <new-session-id>"
			a.toastUntil = time.Now().Add(3 * time.Second)
		} else {
			if err := a.engine.RenameSession(args); err != nil {
				a.addError("rename: " + err.Error())
			} else {
				a.addSystem("session renamed → " + args)
			}
		}
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
	case "/stats":
		a.overlay = overlayStats(a)
	case "/reasoning":
		if args == "" {
			a.overlay = &overlay{title: "Reasoning effort",
				body: fmt.Sprintf("current: %s\n\nlow / medium / high / xhigh", a.engine.ReasoningEffort())}
		} else {
			if err := a.engine.SetReasoningEffort(args); err != nil {
				a.addError(err.Error())
			} else {
				a.addSystem("reasoning effort: " + args)
			}
		}
	case "/theme":
		if args == "" || args == "list" {
			a.overlay = overlayThemes()
		} else {
			parts := strings.Fields(args)
			name := parts[0]
			if applied := SetTheme(name); applied != "" {
				a.addSystem("theme: " + applied)
			} else {
				a.addError("unknown theme: " + name + " (try /theme list)")
			}
		}
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
		a.overlay = overlayMcp(a.engine)
	case "/agents":
		a.overlay = overlayAgents(a.engine)
	case "/skills":
		a.overlay = overlaySkills()
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

// providerArg splits "<provider> <value>" arguments for /set-url, /set-key
// and /set-model. The value keeps everything after the first token so URLs
// and model IDs containing separators still work.
func providerArg(args string) (string, string, error) {
	parts := strings.Fields(args)
	if len(parts) < 2 {
		return "", "", fmt.Errorf("usage: <provider> <value>")
	}
	rest := strings.TrimSpace(strings.TrimPrefix(args, parts[0]))
	return parts[0], rest, nil
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
		if selected == "__new__" {
			a.executeCommand("/new")
		} else {
			a.resumeSession(selected)
		}
	case "Models — pick provider/model":
		parts := strings.SplitN(selected, "|", 2)
		if len(parts) == 2 {
			if err := a.engine.SwitchModel(parts[0], parts[1]); err != nil {
				a.addError(err.Error())
			} else {
				a.addSystem(fmt.Sprintf("switched to %s/%s", parts[0], parts[1]))
			}
		}
	case "Keymap":
		a.keymapCaptureAction = selected
		a.addSystem("keymap: press a key for " + selected + " (esc cancels)")
	case "Backtrack":
		n, err := strconv.Atoi(selected)
		if err != nil || n < 1 {
			a.addError("backtrack: invalid message")
			return
		}
		msg, err := a.engine.BranchBacktrackToUserMessage(n - 1)
		if err != nil {
			a.addError("backtrack: " + err.Error())
			return
		}
		a.items = nil
		a.streaming = nil
		a.composer.SetValue(msg)
		a.composer.Focus()
		a.addSystem(fmt.Sprintf("backtrack: editing message #%d", n))
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
			if isShellTool(it.meta) {
				b.WriteString(stripANSI(codexTranscriptCell(
					codexToolLabel(it.meta, it.args), it.raw,
					it.status == "SUCCEEDED", it.exitCode, it.duration)))
				b.WriteString("\n\n")
			} else {
				fmt.Fprintf(&b, "### Tool: %s (%s)\n\n```\n%s\n```\n\n", it.meta, it.status, it.raw)
			}
		case "system":
			fmt.Fprintf(&b, "_%s_\n\n", it.raw)
		case "bash":
			fmt.Fprintf(&b, "### $ %s\n\n```\n%s\n```\n\n", it.meta, it.raw)
		case "evidence", "unknown", "claim":
			fmt.Fprintf(&b, "_%s_: %s\n\n", it.kind, it.raw)
		case "sep":
			// visual divider — skip in transcript export
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
		a.toolWorkThisTurn = false
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
			// Codex FinalMessageSeparator: a dim rule after turns that
			// performed tool work, labeled with the worked-for time when it
			// is meaningful (>= 1 minute, matching Codex).
			if a.toolWorkThisTurn {
				label := ""
				if dur >= time.Minute {
					label = "Worked for " + fmtDurCompact(dur)
				}
				a.items = append(a.items, &chatItem{
					kind:     "sep",
					rendered: codexSeparator(label, a.widthAvail()-2),
				})
			}
		}
		a.turns++
		a.status = "ready"
	case engine.EvToolStart:
		data, _ := ev.Data.(map[string]any)
		name, _ := data["name"].(string)
		args, _ := data["arguments"].(string)
		it := &chatItem{kind: "tool", meta: name, args: args, status: "running", started: time.Now()}
		a.items = append(a.items, it)
		a.toolWorkThisTurn = true
		a.status = "tool: " + name
	case engine.EvToolStream:
		// Append live output to the most recent running tool card so partial
		// results are visible while the command executes.
		if data, ok := ev.Data.(map[string]any); ok {
			chunk, _ := data["chunk"].(string)
			for i := len(a.items) - 1; i >= 0; i-- {
				it := a.items[i]
				if it.kind == "tool" && it.status == "running" {
					it.raw += chunk
					it.rendered = a.renderToolStreaming(it.meta, it.raw)
					break
				}
			}
		}
	case engine.EvToolEnd:
		data, _ := ev.Data.(map[string]any)
		name, _ := data["name"].(string)
		success, _ := data["success"].(bool)
		output, _ := data["output"].(string)
		exitCode := ""
		if md, ok := data["metadata"].(map[string]any); ok {
			if ec, ok := md["exit_code"].(string); ok {
				exitCode = ec
			}
		}
		// Find the most recent tool-start and update it.
		for i := len(a.items) - 1; i >= 0; i-- {
			if a.items[i].kind == "tool" && a.items[i].meta == name && a.items[i].status == "running" {
				a.items[i].status = statusWord(success)
				a.items[i].raw = output
				a.items[i].exitCode = exitCode
				a.items[i].duration = time.Since(a.items[i].started)
				a.items[i].rendered = a.renderTool(name, success, output)
				break
			}
		}
		if success {
			a.successCnt++
		} else {
			a.failCnt++
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
	case a.loginOverlay != nil:
		main = renderLoginOverlay(a.loginOverlay, a.width-sideW, a.viewportHeight())
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
	b.WriteString(styleDim.Render(">_ "))
	b.WriteString(styleTitle.Render("Astra Harness"))
	b.WriteString(styleDim.Render(" (v0.1.0)"))
	if a.busy {
		b.WriteString("  ")
		b.WriteString(a.spinner.View())
		elapsed := time.Since(a.busyAt).Truncate(time.Second)
		b.WriteString(styleDim.Render(fmt.Sprintf(" %s", elapsed)))
	}
	b.WriteString("  " + styleDim.Render("model:"))
	b.WriteString(" " + styleEmph.Render(a.engine.Model))
	if reasoning := a.engine.ReasoningEffort(); reasoning != "" && reasoning != "medium" {
		b.WriteString(styleDim.Render(" (" + reasoning + ")"))
	}
	b.WriteString("  " + styleDim.Render("directory:"))
	b.WriteString(" " + styleSubtle.Render(headerDir(a.engine.Root)))
	mode := "ask"
	if a.engine.Perm.IsPlanMode() {
		mode = "plan"
	} else if a.engine.Perm.GetMode() == engine.ModeAllow {
		mode = "allow"
	} else if a.engine.Perm.GetMode() == engine.ModeDeny {
		mode = "deny"
	}
	b.WriteString("  " + styleDim.Render("permissions:"))
	b.WriteString(" " + styleSubtle.Render(mode))
	if br := a.engine.Git.BranchOr(""); br != "" {
		b.WriteString("  " + styleDim.Render("branch:"))
		b.WriteString(" " + styleSubtle.Render(br))
	}
	if a.userEmail != "" {
		b.WriteString("  " + styleDim.Render("acct:"))
		b.WriteString(" " + styleSubtle.Render(a.userEmail))
	}
	return styleHeaderRow.Render(b.String())
}

// headerDir shortens the working directory for the session header, replacing
// the user's home prefix with "~" the way Codex's session header does.
func headerDir(root string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return root
	}
	if root == home {
		return "~"
	}
	if strings.HasPrefix(root, home+string(os.PathSeparator)) {
		return "~" + root[len(home):]
	}
	return root
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
	if a.quitArmed {
		left = "ctrl+c again to quit"
	} else if a.escArmed {
		left = "esc esc to edit previous message"
	} else if time.Now().Before(a.toastUntil) {
		left = a.toast
	} else if left == "" {
		left = "? for shortcuts"
	}
	if left == "? for shortcuts" && a.engine.Perm.IsPlanMode() {
		left += " · Plan mode (shift+tab to cycle)"
	}
	right := strings.Join(a.statusLineSegments(), "  ·  ")
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
		}
	}
	// Re-clamp after trimming: truncate() counts runes, not display columns,
	// so wide characters can still push pad negative (strings.Repeat panics).
	if pad < 1 {
		pad = 1
	}
	line := left + strings.Repeat(" ", pad) + right
	return styleStatusBar.Width(a.widthAvail()).Render(line)
}

// statusLineSegments renders the configured Codex-compatible footer items.
func (a *app) statusLineSegments() []string {
	e := a.engine
	var out []string
	usage := a.lastUsage
	total := usage.InputTokens + usage.OutputTokens
	cost := a.totalCost
	if cost <= 0 {
		cost = approximateCost(e.Model, usage)
	}
	reasoning := e.ReasoningEffort()
	for _, id := range e.StatusLineItems() {
		switch id {
		case "model":
			out = append(out, e.Model)
		case "model-with-reasoning":
			m := e.Model
			if reasoning != "" && reasoning != "medium" {
				m = m + " (" + reasoning + ")"
			}
			out = append(out, m)
		case "reasoning":
			if reasoning != "" {
				out = append(out, reasoning)
			}
		case "current-dir", "project-name":
			out = append(out, filepath.Base(e.Root))
		case "git-branch":
			if br := e.Git.BranchOr(""); br != "" {
				out = append(out, br)
			}
		case "run-state":
			if a.busy {
				out = append(out, "Working")
			} else {
				out = append(out, "Ready")
			}
		case "approval-mode":
			out = append(out, e.Perm.GetMode())
		case "permissions":
			mode := e.Perm.GetMode()
			if e.Perm.IsPlanMode() {
				mode = "plan"
			}
			out = append(out, mode)
		case "context-used":
			if max := e.Config.MaxContextTokens; max > 0 && total > 0 {
				pct := total * 100 / max
				out = append(out, fmt.Sprintf("%d%% context used", pct))
			}
		case "context-remaining":
			if max := e.Config.MaxContextTokens; max > 0 && total > 0 {
				pct := total * 100 / max
				if pct > 100 {
					pct = 100
				}
				out = append(out, fmt.Sprintf("%d%% context left", 100-pct))
			}
		case "context-window-size":
			if max := e.Config.MaxContextTokens; max > 0 {
				out = append(out, fmt.Sprintf("%d tok window", max))
			}
		case "used-tokens":
			out = append(out, fmt.Sprintf("%d tok", total))
		case "total-input-tokens":
			out = append(out, fmt.Sprintf("%d in", usage.InputTokens))
		case "total-output-tokens":
			out = append(out, fmt.Sprintf("%d out", usage.OutputTokens))
		case "estimated-cost":
			if cost > 0 {
				out = append(out, fmt.Sprintf("$%.4f", cost))
			}
		case "session-id":
			out = append(out, e.SessionID())
		case "thread-title":
			out = append(out, filepath.Base(e.Root))
		case "task-progress":
			if n := len(e.Store.State.Unknowns); n > 0 {
				out = append(out, fmt.Sprintf("%d tasks", n))
			}
		case "codex-version":
			out = append(out, "astra")
		}
	}
	return out
}

func (a *app) renderPermission() string {
	if a.pendingPerm == nil {
		return ""
	}
	req := a.pendingPerm
	allowKey := a.engine.KeymapBinding("permission_allow")
	alwaysKey := a.engine.KeymapBinding("permission_always")
	denyKey := a.engine.KeymapBinding("permission_deny")
	neverKey := a.engine.KeymapBinding("permission_never")
	var b strings.Builder
	switch req.Kind {
	case engine.PermExecute:
		b.WriteString("Would you like to run the following command?\n")
	case engine.PermRead:
		b.WriteString("Would you like to read the following path?\n")
	case engine.PermWrite:
		b.WriteString("Would you like to modify the following path?\n")
	default:
		b.WriteString("Would you like to grant this permission?\n")
	}
	b.WriteString("\n")
	if req.Description != "" {
		b.WriteString("  Reason: " + styleDim.Render(req.Description) + "\n")
	}
	if req.Risk != "" {
		b.WriteString("  Risk:   " + styleDim.Render(req.Risk) + "\n")
	}
	if req.Command != "" {
		b.WriteString("\n  " + stylePrompt.Render(codexPrompt) + " " + styleCmdName.Render(req.Command) + "\n")
	} else if req.Target != "" {
		b.WriteString("\n  " + styleDim.Render(req.Target) + "\n")
	}
	b.WriteString("\n")
	// Codex-style option list: the recommended action is pre-selected with
	// "›", and every option carries its direct key shortcut.
	b.WriteString("  " + styleKey.Render("›") + " 1. Yes, proceed (" + styleKey.Render(allowKey) + ")\n")
	b.WriteString("     2. Yes, allow for this session (" + styleKey.Render(alwaysKey) + ")\n")
	b.WriteString("     3. No, deny once (" + styleKey.Render(denyKey) + " / esc)\n")
	b.WriteString("     4. No, deny for this session (" + styleKey.Render(neverKey) + ")\n")
	b.WriteString("\n")
	b.WriteString("  " + styleDim.Render("Press y/a/n/N to choose · esc to cancel"))
	return b.String()
}

func (a *app) renderAsk() string {
	if a.pendingAsk == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString(styleTitle.Render("❓ Question from agent") + "\n")
	b.WriteString(a.pendingAsk.question + "\n")
	b.WriteString(styleDim.Render("type an answer and press enter · esc to cancel") + "\n")
	return stylePanel.Width(a.width-2).Render(b.String()) + "\n" + a.composer.View(a.width)
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
	case "assistant":
		return a.renderAssistant(it.raw, it.duration)
	case "tool":
		return a.renderTool(it.meta, it.status == "SUCCEEDED", it.raw)
	case "system":
		return styleSystem.Render("· " + it.raw)
	case "error":
		return styleError.Render("✗ " + it.raw)
	case "evidence", "unknown", "claim":
		return chip(it.kind, it.kind) + " " + styleDim.Render(it.raw)
	case "bash":
		return a.renderBash(it.meta, it.raw, it.status == "SUCCEEDED", it.exitCode, it.duration)
	case "sep":
		return it.rendered
	default:
		return it.raw
	}
}

// renderUser renders a user message the way Codex does: plain text on a
// subtle background tint (user_message_style), with a bold-dim "› " prefix on
// the first line, a 2-column continuation gutter, and no decorative border.
func (a *app) renderUser(text string) string {
	w := max(30, a.widthAvail()-2)
	lines := wrapWords(text, max(30, w-2))
	var b strings.Builder
	b.WriteString(styleUserMsg.Width(w).Render(""))
	b.WriteString("\n")
	for i, l := range lines {
		if i == 0 {
			b.WriteString(styleUserMsg.Width(w).Render(styleUserPre.Render("› ") + styleBody.Render(l)))
		} else {
			b.WriteString(styleUserMsg.Width(w).Render("  " + styleBody.Render(l)))
		}
		if i < len(lines)-1 {
			b.WriteString("\n")
		}
	}
	b.WriteString("\n")
	b.WriteString(styleUserMsg.Width(w).Render(""))
	return b.String()
}

// renderAssistant renders assistant markdown with no surrounding box,
// matching Codex's plain rich-markdown history cells: the first rendered line
// carries a dim "• " bullet and every following line is gutter-indented.
func (a *app) renderAssistant(md string, dur time.Duration) string {
	body := renderMarkdown(md)
	if body == "" {
		return styleBullet.Render(codexBullet) + " " + styleDim.Render("…")
	}
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	var b strings.Builder
	for i, l := range lines {
		// Codex normalizes whitespace-only rows (e.g. the empty line between
		// markdown blocks) so the gutter stays clean instead of a row of spaces.
		if strings.TrimSpace(l) == "" {
			l = ""
		} else {
			l = strings.TrimRight(l, " ")
		}
		if i == 0 {
			b.WriteString(styleBullet.Render(codexBullet+" ") + l)
		} else {
			b.WriteString("  " + l)
		}
		if i < len(lines)-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// renderToolStreaming renders a tool cell that is still producing output,
// Codex-style:
//
//   - Running cargo build
//     └ Compiling foo...
func (a *app) renderToolStreaming(name, output string) string {
	// The item for this live cell holds the tool args (needed for the
	// header label); find the most recent running cell for this tool.
	var args string
	for i := len(a.items) - 1; i >= 0; i-- {
		if a.items[i].kind == "tool" && a.items[i].meta == name && a.items[i].status == "running" {
			args = a.items[i].args
			break
		}
	}
	if output == "" {
		output = "…"
	}
	return codexExecCell(a.widthAvail()-2, true, false, name, args, output, toolMaxLines)
}

// renderTool renders a committed tool call as a Codex history cell:
//
//   - Ran go test ./...          ← run_command / run_tests / run_build
//     └ ok  github.com/x 3.4s
//
//   - Called search({"q":"x"})   ← non-shell tools
//     └ 3 results
func (a *app) renderTool(name string, success bool, output string) string {
	collapsed := false
	for i := len(a.items) - 1; i >= 0; i-- {
		if a.items[i].kind == "tool" && a.items[i].status != "running" {
			collapsed = a.items[i].collapsed
			break
		}
	}
	if collapsed {
		return styleToolBox.Width(a.widthAvail() - 2).Render(
			styleDim.Render("(collapsed — press x or ⌘O to expand)"))
	}
	// Find the matching item for the tool arguments used in the header label.
	args := ""
	for i := len(a.items) - 1; i >= 0; i-- {
		if a.items[i].kind == "tool" && a.items[i].meta == name {
			args = a.items[i].args
			break
		}
	}
	return codexExecCell(a.widthAvail()-2, false, success, name, args, output, toolMaxLines)
}

// renderBash renders the user shell (bang mode) result, Codex-style:
//
//   - You ran ls
//     └ file1
//     file2
func (a *app) renderBash(cmd, output string, ok bool, exitCode string, dur time.Duration) string {
	return codexUserShellCell(cmd, output, a.widthAvail()-2)
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
