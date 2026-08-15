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
)

type chatItem struct {
	kind      string // user | assistant | tool | system | error | evidence | unknown | claim
	meta      string
	raw       string
	rendered  string
	status    string
	collapsed bool
}

type streamingMsg struct {
	model string
	raw   string
}

type engineEventMsg struct {
	ev engine.Event
}

type indexDoneMsg struct {
	err error
}

type verifyDoneMsg struct {
	success bool
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
	lastToolIdx   int
	composer      composer
	overlay       *overlay
	spinner       spinner.Model

	mode        string
	busy        bool
	pendingPerm *engine.PermissionRequest
	pendingAsk  *askState
	status      string
	toast       string
	toastUntil  time.Time
	lastUsage   string
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
		spinner: sp, mode: modeChat, lastToolIdx: -1,
	}
	a.vp = viewport.New(80, 24)
	a.vp.Style = lipgloss.NewStyle().Padding(0, 1)
	a.composer = newComposer(80)
	a.addSystem(fmt.Sprintf("Astra Harness — %s · %s/%s", filepath.Base(root), eng.ProviderID(), eng.Model))
	a.addSystem(eng.Index.Stats())
	return a
}

// Run starts the Bubble Tea program.
func Run(root string, cfg *engine.Config, eng *engine.Engine) error {
	a := NewApp(root, cfg, eng)
	p := tea.NewProgram(a, tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err := p.Run()
	return err
}

func (a *app) Init() tea.Cmd {
	return tea.Batch(a.spinner.Tick, a.waitEvent)
}

func (a *app) waitEvent() tea.Msg {
	return engineEventMsg{ev: <-a.events}
}

func (a *app) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		a.width = m.Width
		a.height = m.Height
		setMarkdownWidth(m.Width)
		a.vp.Width = m.Width - 2
		a.vp.Height = a.viewportHeight()
		a.composer.SetWidth(m.Width)
		a.refreshViewport()
		return a, nil
	case spinner.TickMsg:
		var cmd tea.Cmd
		a.spinner, cmd = a.spinner.Update(msg)
		return a, cmd
	case engineEventMsg:
		return a, a.handleEngineEvent(m.ev)
	case indexDoneMsg:
		a.busy = false
		if m.err != nil {
			a.addError("index failed: " + m.err.Error())
		} else {
			a.addSystem("index rebuilt: " + a.engine.Index.Stats())
		}
		a.refreshViewport()
		return a, nil
	case verifyDoneMsg:
		a.busy = false
		a.addSystem("verify " + statusWord(m.success))
		a.refreshViewport()
		return a, nil
	case tea.MouseMsg:
		a.vp, _ = a.vp.Update(msg)
		return a, nil
	case tea.KeyMsg:
		return a.handleKey(m)
	default:
		return a, nil
	}
}

func (a *app) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if a.quit {
		return a, tea.Quit
	}
	s := msg.String()

	if a.overlay != nil {
		closed, selected, handled := a.overlay.update(msg)
		if handled {
			if closed {
				if selected != "" && a.overlay.selectedID != "" {
					a.overlaySelect(selected)
				}
				a.overlay = nil
				a.refreshViewport()
			}
			return a, nil
		}
	}

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
		text, submit, handled := a.composer.update(msg)
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
		_ = handled
		return a, nil
	}

	// chat mode
	switch s {
	case "ctrl+c":
		if a.busy {
			a.engine.Stop()
			a.status = "stopping agent..."
			return a, nil
		}
		a.quit = true
		return a, tea.Quit
	case "ctrl+u":
		a.vp.LineUp(10)
		return a, nil
	case "ctrl+d":
		a.vp.LineDown(10)
		return a, nil
	case "pgup", "pgdown":
		a.vp, _ = a.vp.Update(msg)
		return a, nil
	case "x":
		if a.lastToolIdx >= 0 && a.lastToolIdx < len(a.items) {
			a.items[a.lastToolIdx].collapsed = !a.items[a.lastToolIdx].collapsed
			a.refreshViewport()
		}
		return a, nil
	}

	text, submit, _ := a.composer.update(msg)
	if submit {
		if a.busy {
			a.toast = "agent is running — press ctrl+c to stop"
			a.toastUntil = time.Now().Add(4 * time.Second)
			return a, nil
		}
		if strings.HasPrefix(strings.TrimSpace(text), "/") {
			cmd := a.executeCommand(strings.TrimSpace(text))
			return a, cmd
		}
		cmd := a.startAgent(strings.TrimSpace(text))
		return a, cmd
	}
	return a, nil
}

func (a *app) startAgent(prompt string) tea.Cmd {
	a.items = append(a.items, &chatItem{kind: "user", raw: prompt, rendered: a.renderUser(prompt)})
	a.busy = true
	a.status = "starting agent..."
	a.streaming = nil
	a.refreshViewport()
	return func() tea.Msg {
		_ = a.engine.Run(context.Background(), prompt)
		return nil
	}
}

func (a *app) executeCommand(cmdline string) tea.Cmd {
	parts := strings.Fields(cmdline)
	cmd := parts[0]
	args := ""
	if len(parts) > 1 {
		args = strings.TrimSpace(strings.TrimPrefix(cmdline, cmd))
	}
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
	case "/goal":
		if args != "" {
			g := a.engine.SetGoal(args, nil)
			a.addSystem(fmt.Sprintf("goal set: %s", g.Description))
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
	case "/permissions":
		if args != "" {
			a.engine.Perm.SetMode(args)
			a.addSystem("permission mode: " + args)
		} else {
			a.overlay = &overlay{title: "Permissions",
				body: fmt.Sprintf("current mode: %s\n\n  /permissions allow  — auto-approve\n  /permissions ask     — prompt each time\n  /permissions deny    — block writes/execution", a.engine.Perm.GetMode())}
		}
	case "/plan":
		on := !a.engine.Perm.IsPlanMode()
		a.engine.Perm.SetPlanMode(on)
		a.addSystem(fmt.Sprintf("plan mode: %v (write/execute blocked)", on))
	case "/init", "/index":
		a.addSystem("indexing repository...")
		a.busy = true
		return func() tea.Msg {
			return indexDoneMsg{err: a.engine.RebuildIndex()}
		}
	case "/verify":
		a.addSystem("verifying (tests/build)...")
		a.busy = true
		return func() tea.Msg {
			res := a.engine.Verify(context.Background())
			return verifyDoneMsg{success: res.Success}
		}
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
	case "/clear":
		a.items = nil
		a.streaming = nil
		a.lastToolIdx = -1
		a.addSystem("chat cleared — durable state kept in .astra/")
	case "/new":
		a.items = nil
		a.streaming = nil
		a.lastToolIdx = -1
		a.addSystem("new session — state and sessions remain in .astra/")
	case "/quit", "/exit", "/q":
		a.quit = true
		return tea.Quit
	default:
		a.addError("unknown command: " + cmd + " (try /help)")
	}
	a.refreshViewport()
	return nil
}

func (a *app) overlaySelect(selected string) {
	id := a.overlay.selectedID
	if id == "" {
		return
	}
	switch a.overlay.title {
	case "Sessions":
		a.resumeSession(strings.TrimSpace(id))
	case "Models — pick provider/model":
		parts := strings.SplitN(id, "|", 2)
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
	a.lastToolIdx = -1
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
			a.streaming = &streamingMsg{model: a.engine.Model}
		}
		if text, ok := ev.Data.(string); ok {
			a.streaming.raw += text
		}
	case engine.EvAssistantStart:
		a.streaming = &streamingMsg{model: a.engine.Model}
		a.status = "agent thinking..."
	case engine.EvAssistantEnd:
		if a.streaming != nil {
			data, _ := ev.Data.(map[string]any)
			content, _ := data["content"].(string)
			a.items = append(a.items, &chatItem{
				kind: "assistant", meta: a.engine.Model, raw: content,
				rendered: a.renderAssistant(content),
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
		a.lastToolIdx = len(a.items) - 1
		a.status = "tool: " + name
	case engine.EvToolEnd:
		data, _ := ev.Data.(map[string]any)
		name, _ := data["name"].(string)
		success, _ := data["success"].(bool)
		output, _ := data["output"].(string)
		if a.lastToolIdx >= 0 && a.lastToolIdx < len(a.items) {
			it := a.items[a.lastToolIdx]
			it.status = statusWord(success)
			it.raw = output
			it.rendered = a.renderTool(name, success, output)
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
		a.addChip("evidence", fmt.Sprintf("%s · %s", evidenceShort(ev.Data), time.Now().Format("15:04:05")))
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
			a.lastUsage = fmt.Sprintf("%d→%d tok", u.InputTokens, u.OutputTokens)
		}
	case engine.EvDone:
		a.busy = false
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
	}
	a.refreshViewport()
	return nil
}

// View -----------------------------------------------------------------------

func (a *app) View() string {
	if a.quit {
		return ""
	}
	header := a.renderHeader()
	var main string
	if a.overlay != nil {
		main = a.overlay.View(a.width, a.viewportHeight())
	} else {
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
		composerView = a.composer.View(a.width)
	}
	return header + "\n" + main + "\n" + composerView + "\n" + footer
}

func (a *app) renderHeader() string {
	var b strings.Builder
	b.WriteString(styleTitle.Render("◆ Astra"))
	b.WriteString(" " + styleDim.Render("uncertainty-driven runtime"))
	b.WriteString("  " + a.spinner.View())
	if a.busy {
		b.WriteString(styleDim.Render("working…"))
	}
	b.WriteString("  " + styleDim.Render(fmt.Sprintf("%s/%s", a.engine.ProviderID(), a.engine.Model)))
	mode := "normal"
	if a.engine.Perm.IsPlanMode() {
		mode = "plan"
	}
	b.WriteString("  " + styleDim.Render("["+mode+"]"))
	if a.engine.Git.Branch() != "" {
		b.WriteString("  " + styleDim.Render("⎇ "+a.engine.Git.Branch()))
	}
	return lipgloss.NewStyle().Padding(0, 1).Render(b.String())
}

func (a *app) renderStatusBar() string {
	left := a.status
	if left == "" {
		left = "ready — describe a task or type /help"
	}
	if time.Now().Before(a.toastUntil) {
		left = a.toast
	}
	right := fmt.Sprintf("claims %d · unknowns %d · evidence %d",
		len(a.engine.Store.State.Claims), len(a.engine.Store.State.Unknowns), len(a.engine.Store.State.Evidence))
	if a.lastUsage != "" {
		right += " · " + a.lastUsage
	}
	line := lipgloss.NewStyle().Width(a.width - 2).Render(left + strings.Repeat(" ", maxInt(0, a.width-2-lipgloss.Width(left)-lipgloss.Width(right))) + right)
	return styleStatusBar.Width(a.width).Render(line)
}

func (a *app) renderPermission() string {
	if a.pendingPerm == nil {
		return ""
	}
	req := a.pendingPerm
	var b strings.Builder
	b.WriteString(styleTitle.Render("Permission required · "+req.Kind) + "\n")
	b.WriteString("target:     " + req.Target + "\n")
	if req.Command != "" {
		b.WriteString("command:    " + req.Command + "\n")
	}
	if req.Description != "" {
		b.WriteString("description: " + req.Description + "\n")
	}
	b.WriteString(styleDim.Render(req.Risk) + "\n")
	b.WriteString("y allow · a always allow · n deny · N always deny · esc deny once")
	return styleComposer.Width(a.width - 2).Render(b.String())
}

func (a *app) renderAsk() string {
	if a.pendingAsk == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString(styleTitle.Render("Question from agent") + "\n")
	b.WriteString(a.pendingAsk.question + "\n")
	b.WriteString(styleDim.Render("type an answer and press enter · esc to cancel") + "\n")
	return styleComposer.Width(a.width-2).Render(b.String()) + "\n" + a.composer.View(a.width)
}

func (a *app) viewportHeight() int {
	h := a.height - 8
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
		parts = append(parts, a.renderAssistant(a.streaming.raw))
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
		return chip(it.kind) + " " + styleDim.Render(it.raw)
	default:
		return it.raw
	}
}

func (a *app) renderUser(text string) string {
	inner := lipgloss.NewStyle().Foreground(white).Width(maxInt(30, a.width-8)).Render(text)
	return styleUserBox.Width(a.width - 4).Render(styleTitle.Render("You") + "\n\n" + inner)
}

func (a *app) renderAssistant(md string) string {
	body := renderMarkdown(md)
	title := styleTitle.Render("Astra") + " " + styleDim.Render("· "+a.engine.Model)
	if body == "" {
		body = styleDim.Render("…")
	}
	return styleAssistantBox.Width(a.width - 4).Render(title + "\n\n" + body)
}

func (a *app) renderTool(name string, success bool, output string) string {
	statusColor := styleError
	statusText := "FAILED"
	if success {
		statusColor = styleSuccess
		statusText = "SUCCEEDED"
	}
	title := styleTitle.Render("⌘ "+name) + " " + statusColor.Render(statusText)
	body := output
	if len(body) > 1600 {
		body = body[:1600] + "\n… (+" + fmt.Sprint(len(output)-1600) + " chars — press x to toggle)"
	}
	if a.lastToolIdx >= 0 && a.items[a.lastToolIdx].collapsed {
		body = styleDim.Render("(collapsed — press x to expand)")
	}
	return styleToolBox.Width(a.width - 4).Render(title + "\n\n" + lipgloss.NewStyle().Width(a.width-8).Render(body))
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

// Small helpers --------------------------------------------------------------

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

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
