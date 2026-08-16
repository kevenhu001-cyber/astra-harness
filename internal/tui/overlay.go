package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/kevenhu001-cyber/astra-harness/internal/core"
	"github.com/kevenhu001-cyber/astra-harness/internal/engine"
	"github.com/kevenhu001-cyber/astra-harness/internal/knowledge"
)

// overlay is a generic centered overlay with an optional left list + right
// detail. The list selection drives the detail panel.
type overlay struct {
	title    string
	footer   string
	items    []string
	detail   []string // one row of detail per item (rendered when list focused)
	body     string   // fallback body when no items
	sel      int
	tabs     []string
	tab      int
	onSelect func(string) string
}

// ensure overlay has unique rendering helpers.
func (o *overlay) empty() bool { return len(o.items) == 0 && o.body == "" }

func (o *overlay) update(msg tea.Msg) (closed bool, selected string, handled bool) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return false, "", false
	}
	switch key.String() {
	case "esc", "ctrl+c":
		return true, "", true
	case "up", "shift+tab":
		if len(o.items) > 0 {
			o.sel = (o.sel - 1 + len(o.items)) % len(o.items)
		}
		return false, "", true
	case "down", "tab":
		if len(o.items) > 0 {
			o.sel = (o.sel + 1) % len(o.items)
		}
		return false, "", true
	case "left":
		if len(o.tabs) > 0 {
			o.tab = (o.tab - 1 + len(o.tabs)) % len(o.tabs)
		}
		return false, "", true
	case "right":
		if len(o.tabs) > 0 {
			o.tab = (o.tab + 1) % len(o.tabs)
		}
		return false, "", true
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		idx := int(key.String()[0]-'1')
		if idx < len(o.tabs) {
			o.tab = idx
		}
		return false, "", true
	case "enter":
		if len(o.items) > 0 && o.onSelect != nil {
			id := o.onSelect(o.items[o.sel])
			return true, id, true
		}
		return true, "", true
	case "q":
		return true, "", true
	}
	return false, "", false
}

func (o *overlay) View(width, height int) string {
	if o.body != "" && len(o.items) == 0 {
		return renderSimpleOverlay(o, width, height)
	}
	return renderTabbedOverlay(o, width, height)
}

func renderSimpleOverlay(o *overlay, width, height int) string {
	maxW := width - 8
	if maxW > 120 {
		maxW = 120
	}
	var b strings.Builder
	b.WriteString(styleTitle.Render(" "+o.title) + "\n\n")
	b.WriteString(o.body)
	b.WriteString("\n\n")
	if o.footer != "" {
		b.WriteString(styleDim.Render(o.footer))
	} else {
		b.WriteString(styleDim.Render("esc close"))
	}
	box := styleOverlay.Width(maxW).Render(b.String())
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box)
}

func renderTabbedOverlay(o *overlay, width, height int) string {
	maxW := width - 6
	if maxW > 130 {
		maxW = 130
	}
	// Two-column layout: list (left) + detail (right).
	leftW := maxW/2 - 2
	if leftW < 28 {
		leftW = 28
	}
	rightW := maxW - leftW - 6

	// Tabs row.
	var tabs strings.Builder
	if len(o.tabs) > 0 {
		for i, t := range o.tabs {
			label := "  " + t + "  "
			if i == o.tab {
				tabs.WriteString(styleKey.Render(" "+t+" "))
			} else {
				tabs.WriteString(styleDim.Render(label))
			}
			tabs.WriteString(styleDim.Render("  "))
		}
	}

	var b strings.Builder
	b.WriteString(styleTitle.Render("◆ "+o.title))
	b.WriteString("\n")
	if tabs.Len() > 0 {
		b.WriteString(tabs.String())
		b.WriteString("\n")
	}
	b.WriteString(styleFaint.Render(strings.Repeat("─", maxW-4)))
	b.WriteString("\n\n")

	left := renderList(o, leftW)
	right := renderDetail(o, rightW)

	// Combine left + right side-by-side.
	combined := lipgloss.JoinHorizontal(lipgloss.Top,
		lipgloss.NewStyle().Width(leftW).Render(left),
		"  ",
		lipgloss.NewStyle().Width(rightW).Render(right),
	)
	b.WriteString(combined)
	b.WriteString("\n")
	if o.footer != "" {
		b.WriteString(styleDim.Render(o.footer))
	} else {
		b.WriteString(styleDim.Render("↑↓ select · ⏎ open · esc close"))
	}
	box := styleOverlay.Width(maxW).Render(b.String())
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box)
}

func renderList(o *overlay, w int) string {
	var b strings.Builder
	maxRows := 18
	for i, it := range o.items {
		if i >= maxRows {
			b.WriteString(styleDim.Render(fmt.Sprintf("  …+%d more", len(o.items)-i)))
			break
		}
		text := truncate(it, w-4)
		if i == o.sel {
			b.WriteString(styleTitle.Render("● " + text))
		} else {
			b.WriteString(styleBody.Render("  " + text))
		}
		b.WriteString("\n")
	}
	if len(o.items) == 0 {
		b.WriteString(styleDim.Render("(no items)"))
	}
	return b.String()
}

func renderDetail(o *overlay, w int) string {
	if len(o.detail) > o.sel && o.detail[o.sel] != "" {
		return o.detail[o.sel]
	}
	return styleDim.Render("No detail")
}

// ---- factories ----------------------------------------------------------

func overlayStatus(e *engine.Engine) *overlay {
	st := e.Store.State
	var b strings.Builder
	b.WriteString(e.CompilerOutput())
	b.WriteString("\n")
	b.WriteString(styleDim.Render(fmt.Sprintf("state dir: %s\nindex: %s", e.StateDir(), e.IndexPath())))
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("goals=%d claims=%d evidence=%d unknowns=%d actions=%d",
		len(st.Goals), len(st.Claims), len(st.Evidence), len(st.Unknowns), len(st.Actions)))
	return &overlay{title: "Status — compiled knowledge state", body: b.String()}
}

func overlayClaims(st *core.State) *overlay {
	if len(st.Claims) == 0 {
		return &overlay{title: "Claims", body: "No claims yet.\n\nClaims emerge from verification, tests, builds, and runtime evidence."}
	}
	type row struct {
		label string
		order int
		detail string
	}
	rows := make([]row, 0, len(st.Claims))
	for _, c := range st.Claims {
		rows = append(rows, row{
			label:  fmt.Sprintf("[%s] %.0f%%  %s %s %s", c.Status, c.Confidence*100, c.Subject, c.Predicate, c.Object),
			order:  claimStatusOrder(c),
			detail: formatClaimDetail(c, st),
		})
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].order < rows[j].order })
	o := &overlay{title: "Claims", tabs: []string{"By status", "By confidence"}}
	for _, r := range rows {
		o.items = append(o.items, r.label)
		o.detail = append(o.detail, r.detail)
	}
	return o
}

func claimStatusOrder(c *core.Claim) int {
	switch c.Status {
	case core.ClaimVerified:
		return 0
	case core.ClaimSupported:
		return 1
	case core.ClaimHypothesis:
		return 2
	case core.ClaimUnknown:
		return 3
	case core.ClaimContradicted:
		return 4
	case core.ClaimStale:
		return 5
	}
	return 99
}

func formatClaimDetail(c *core.Claim, st *core.State) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Subject:    %s\nPredicate:  %s\nObject:     %s\nType:       %s\nStatus:     %s\nConfidence: %.0f%%\nSource:     %s\nCodeState:  %s\nCreated:    %s\n",
		c.Subject, c.Predicate, c.Object, c.ClaimType, c.Status, c.Confidence*100, c.Source, c.CodeState, c.CreatedAt.Format(time.RFC3339))
	b.WriteString("\nLinked Evidence:\n")
	byID := map[string]*core.Evidence{}
	for _, ev := range st.Evidence {
		byID[ev.ID] = ev
	}
	if len(c.EvidenceIDs) == 0 {
		b.WriteString(styleDim.Render("  (no evidence — still HYPOTHESIS)"))
	} else {
		for _, id := range c.EvidenceIDs {
			if ev, ok := byID[id]; ok {
				fmt.Fprintf(&b, "  • [%s] %s · %s · conf %.0f%%\n",
					ev.Kind, ev.Source, ev.Status, ev.Confidence*100)
				summary := ev.Content
				if len(summary) > 240 {
					summary = summary[:237] + "..."
				}
				b.WriteString("      ")
				b.WriteString(styleDim.Render(strings.ReplaceAll(summary, "\n", " ")))
				b.WriteString("\n")
			}
		}
	}
	return b.String()
}

func overlayUnknowns(st *core.State) *overlay {
	unknowns := core.RankUnknowns(st.Unknowns)
	if len(unknowns) == 0 {
		return &overlay{title: "Unknowns", body: "No open unknowns. Nice — but verify before declaring done."}
	}
	o := &overlay{title: "Unknowns — ranked by priority", tabs: []string{"Ranked", "By source"}}
	for _, u := range unknowns {
		o.items = append(o.items, fmt.Sprintf("p=%.2f  %s", u.Priority, truncate(u.Description, 60)))
		o.detail = append(o.detail, formatUnknownDetail(u))
	}
	return o
}

func formatUnknownDetail(u *core.Unknown) string {
	bar := strings.Repeat("▰", int(u.Priority*10)) + strings.Repeat("▱", 10-int(u.Priority*10))
	return fmt.Sprintf("Description:    %s\nImpact:          %.0f%%\nConfidence:      %.0f%%\nUncertainty:     %.0f%%\nCost (resolve):  %.0f%%\nDependency:      %.0f%%\nSource:          %s\nStatus:          %s\nPriority:        %.3f\n\n%s\n\nCreated: %s",
		u.Description, u.Impact*100, u.Confidence*100, u.Uncertainty()*100,
		u.ResolutionCost*100, u.DependencyWeight*100, u.Source, u.Status, u.Priority,
		bar, u.CreatedAt.Format(time.RFC3339))
}

func overlayEvidence(st *core.State) *overlay {
	if len(st.Evidence) == 0 {
		return &overlay{title: "Evidence", body: "No evidence recorded yet."}
	}
	o := &overlay{title: "Evidence", tabs: []string{"All", "Tests", "Build", "Runtime"}}
	for _, ev := range st.Evidence {
		o.items = append(o.items, fmt.Sprintf("[%s] %.0f%%  %s", ev.Kind, ev.Confidence*100, truncate(ev.Source, 50)))
		o.detail = append(o.detail, formatEvidenceDetail(ev))
	}
	sort.SliceStable(o.items, func(i, j int) bool { return o.items[i] < o.items[j] })
	return o
}

func formatEvidenceDetail(ev *core.Evidence) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Kind:        %s\nSource:      %s\nStatus:      %s\nConfidence:  %.0f%%\nCodeState:   %s\nCommit:      %s\nCreated:     %s\n\n",
		ev.Kind, ev.Source, ev.Status, ev.Confidence*100, ev.CodeState, ev.Commit, ev.CreatedAt.Format(time.RFC3339))
	b.WriteString("Content:\n")
	content := ev.Content
	if len(content) > 2400 {
		content = content[:2397] + "..."
	}
	b.WriteString(strings.ReplaceAll(content, "\n", "\n"))
	return b.String()
}

func overlayActions(st *core.State) *overlay {
	if len(st.Actions) == 0 {
		return &overlay{title: "Actions", body: "No actions recorded."}
	}
	actions := append([]*core.Action(nil), st.Actions...)
	sort.SliceStable(actions, func(i, j int) bool { return actions[i].CreatedAt.After(actions[j].CreatedAt) })
	o := &overlay{title: "Actions", tabs: []string{"Recent", "By utility"}}
	for _, a := range actions {
		o.items = append(o.items, fmt.Sprintf("[%s] %s  u=%.2f", a.Status, truncate(a.Description, 60), a.Utility))
		o.detail = append(o.detail, formatActionDetail(a))
	}
	return o
}

func formatActionDetail(a *core.Action) string {
	return fmt.Sprintf("Description:         %s\nType:                 %s\nTool:                 %s\nStatus:               %s\nExpectedInfoGain:     %.2f\nExpectedGoalProgress: %.2f\nCost:                 %.2f\nRisk:                 %.2f\nUtility:              %.3f\nStarted:              %s\nFinished:             %s\n\nResult:\n%s",
		a.Description, a.Type, a.Tool, a.Status,
		a.ExpectedInfoGain, a.ExpectedGoalProgress, a.Cost, a.Risk, a.Utility,
		a.StartedAt.Format(time.RFC3339), a.FinishedAt.Format(time.RFC3339),
		strings.ReplaceAll(a.ResultSummary, "\n", "\n"))
}

func overlayEvents(e *engine.Engine) *overlay {
	events := e.Store.State.Events
	if len(events) == 0 {
		return &overlay{title: "Events (materialized)", body: "No events yet."}
	}
	o := &overlay{title: "Event log", tabs: []string{"Recent", "By type"}}
	start := 0
	if len(events) > 80 {
		start = len(events) - 80
	}
	for _, ev := range events[start:] {
		o.items = append(o.items, fmt.Sprintf("%s  %s", ev.Timestamp.Format("15:04:05.000"), ev.Type))
		o.detail = append(o.detail, fmt.Sprintf("Type: %s\nID:   %s\nTime: %s\n\nData:\n%s",
			ev.Type, ev.ID, ev.Timestamp.Format(time.RFC3339), prettyJSON(ev.Data)))
	}
	sort.SliceStable(o.items, func(i, j int) bool { return o.items[i] > o.items[j] })
	return o
}

func overlaySessions(e *engine.Engine) *overlay {
	sessions, _ := e.Store.ListSessions()
	o := &overlay{title: "Sessions", tabs: []string{"Recent", "By model"}}
	if len(sessions) == 0 {
		o.body = "No saved sessions. Run a task to create one."
		return o
	}
	for _, s := range sessions {
		o.items = append(o.items, fmt.Sprintf("%s · %d msgs · %s",
			truncate(s.ID, 18), len(s.Messages), s.UpdatedAt.Format("01-02 15:04")))
		o.detail = append(o.detail, fmt.Sprintf("ID:        %s\nCreated:   %s\nUpdated:   %s\nModel:     %s\nMessages:  %d\nGoal:      %s",
			s.ID, s.CreatedAt.Format(time.RFC3339), s.UpdatedAt.Format(time.RFC3339),
			s.Model, len(s.Messages), s.GoalID))
	}
	o.onSelect = func(sel string) string {
		id := strings.Fields(sel)[0]
		return id
	}
	return o
}

func overlayModels(e *engine.Engine) *overlay {
	o := &overlay{title: "Models — pick provider/model", tabs: []string{"Available", "Configured"}}
	for _, p := range e.Router.Providers() {
		marker := "○"
		if p.ID() == e.ProviderID() {
			marker = "●"
		}
		avail := styleDim.Render("[no key]")
		if p.Available() {
			avail = styleOk.Render("[ready]")
		}
		for _, m := range p.Models() {
			o.items = append(o.items, fmt.Sprintf("%s %s/%s  %s", marker, p.ID(), m, avail))
			defaultMark := ""
			if p.DefaultModel() == m {
				defaultMark = " (default)"
			}
			o.detail = append(o.detail, fmt.Sprintf("Provider:  %s\nID:        %s\nModel:     %s%s\nAvailable: %v\nPricing:   %s\n\nPricing reflects USD per million tokens. Local models (Ollama) are 0.",
				p.Name(), p.ID(), m, defaultMark, p.Available(), describePricing(m)))
		}
	}
	o.onSelect = func(sel string) string {
		fields := strings.Fields(sel)
		if len(fields) < 2 {
			return ""
		}
		model := fields[len(fields)-1]
		provider := fields[len(fields)-2]
		return provider + "|" + model
	}
	return o
}

func describePricing(model string) string {
	p := pricingFor(model)
	if p.Input == 0 && p.Output == 0 {
		return "free / local"
	}
	return fmt.Sprintf("in $%.2f / out $%.2f", p.Input, p.Output)
}

func overlayHelp() *overlay {
	cat := "Session"
	cats := []slashCmd{}
	for _, c := range slashCommands {
		if c.Category != cat {
			cat = c.Category
		}
		cats = append(cats, c)
	}
	o := &overlay{title: "Help", tabs: append([]string{}, slashCategories...)}
	byCat := map[string][]slashCmd{}
	for _, c := range slashCommands {
		cat := c.Category
		if cat == "" {
			cat = "Help"
		}
		byCat[cat] = append(byCat[cat], c)
	}
	for _, c := range slashCategories {
		for _, cmd := range byCat[c] {
			o.items = append(o.items, fmt.Sprintf("%-14s  %s", cmd.Name, cmd.Desc))
			shortcut := ""
			if cmd.Shortcut != "" {
				shortcut = "  (" + cmd.Shortcut + ")"
			}
			o.detail = append(o.detail, fmt.Sprintf(
				"Command:     %s\nCategory:    %s\nDescription: %s%s\n\nUsage: type / then press Tab to autocomplete.",
				cmd.Name, cmd.Category, cmd.Desc, shortcut))
		}
	}
	return o
}

func overlayDiff(g *knowledge.Git) *overlay {
	return &overlay{title: "Git diff", body: g.Diff()}
}

func overlayDebug(e *engine.Engine) *overlay {
	var b strings.Builder
	fmt.Fprintf(&b, "root:        %s\n", e.Root)
	fmt.Fprintf(&b, "provider:    %s\n", e.ProviderID())
	fmt.Fprintf(&b, "model:       %s\n", e.Model)
	fmt.Fprintf(&b, "perm mode:   %s\n", e.Perm.GetMode())
	fmt.Fprintf(&b, "plan mode:   %v\n", e.Perm.IsPlanMode())
	fmt.Fprintf(&b, "state dir:   %s\n", e.StateDir())
	fmt.Fprintf(&b, "index:       %s\n", e.IndexPath())
	fmt.Fprintf(&b, "index stats: %s\n", e.Index.Stats())
	fmt.Fprintf(&b, "session:     %s\n", e.SessionID())
	pr := pricingFor(e.Model)
	fmt.Fprintf(&b, "model cost:  in $%.2f / out $%.2f per 1M tok\n", pr.Input, pr.Output)
	return &overlay{title: "Debug info", body: b.String()}
}

func overlayCost(e *engine.Engine) *overlay {
	u := e.Usage()
	cost := approximateCost(e.Model, u)
	pr := pricingFor(e.Model)
	var b strings.Builder
	fmt.Fprintf(&b, "Model:           %s\nProvider:        %s\n", e.Model, e.ProviderID())
	fmt.Fprintf(&b, "Input tokens:    %d\nOutput tokens:   %d\nTotal tokens:    %d\n", u.InputTokens, u.OutputTokens, u.InputTokens+u.OutputTokens)
	fmt.Fprintf(&b, "\nPricing (per 1M tok):\n  input:   $%.2f\n  output:  $%.2f\n", pr.Input, pr.Output)
	fmt.Fprintf(&b, "\nEstimated cost:  $%.4f\n", cost)
	return &overlay{title: "Token usage & cost", body: b.String()}
}

func overlayGoal(e *engine.Engine) *overlay {
	g := e.Store.ActiveGoal()
	if g == nil {
		return &overlay{title: "Goal", body: "No active goal.\n\nSet one with /goal <description>"}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "[%s] progress %.0f%%\n\n%s\n", g.Status, g.Progress*100, g.Description)
	if len(g.AcceptanceCriteria) > 0 {
		b.WriteString("\nAcceptance Criteria:\n")
		for i, c := range g.AcceptanceCriteria {
			fmt.Fprintf(&b, "  %d. %s\n", i+1, c)
		}
	} else {
		b.WriteString("\nAcceptance Criteria: (none — add them with /goal <description> [criteria...])")
	}
	b.WriteString(fmt.Sprintf("\nCreated: %s\nUpdated: %s", g.CreatedAt.Format(time.RFC3339), g.UpdatedAt.Format(time.RFC3339)))
	return &overlay{title: "Active goal", body: b.String()}
}

func overlayTree(e *engine.Engine) *overlay {
	var b strings.Builder
	files := scanProjectFiles(e.Root)
	if len(files) == 0 {
		b.WriteString("(empty)")
	}
	for _, f := range files {
		if len(b.String()) > 6000 {
			fmt.Fprintf(&b, "...+%d more files\n", len(files))
			break
		}
		b.WriteString(f)
		b.WriteString("\n")
	}
	return &overlay{title: "Project tree", body: b.String()}
}

func overlayMcp() *overlay {
	return &overlay{
		title: "MCP servers",
		body: "No MCP servers configured.\n\nAstra supports the Model Context Protocol as an external tool adapter.\n\nAdd servers later via ~/.config/astra/mcp.json:\n\n{\n  \"servers\": {\n    \"github\": {\n      \"type\": \"stdio\",\n      \"command\": \"mcp-github\",\n      \"args\": []\n    }\n  }\n}\n\n(Placeholder — full MCP adapter ships in a follow-up.)",
	}
}

func overlayTasks(e *engine.Engine) *overlay {
	st := e.Store.State
	var b strings.Builder
	if g := e.Store.ActiveGoal(); g != nil {
		b.WriteString(styleTitle.Render("Active goal"))
		b.WriteString("\n")
		b.WriteString(g.Description)
		b.WriteString("\n\n")
	}
	b.WriteString(styleTitle.Render("Todos (auto-derived)"))
	b.WriteString("\n")
	if len(st.Unknowns) == 0 {
		b.WriteString(styleDim.Render("  (no unknowns — nothing to investigate)"))
	}
	for i, u := range core.RankUnknowns(st.Unknowns) {
		if i > 12 {
			break
		}
		fmt.Fprintf(&b, "[ ] %s   (p=%.2f)\n", u.Description, u.Priority)
	}
	return &overlay{title: "Tasks", body: b.String()}
}

func overlayAgents(e *engine.Engine) *overlay {
	return &overlay{
		title: "Sub-agents",
		body: "Sub-agents are spawned from the main loop when an action requires\nisolation or a different model.\n\nFuture:\n  /spawn <task>  — create a sub-agent in its own session\n  /agents        — list active sub-agents\n  /kill <id>     — terminate a sub-agent\n\n(Implemented in Phase 11/12.)",
	}
}

func overlayThemes() *overlay {
	return &overlay{
		title: "Themes",
		body: "• astra-dark  (default) — deep space + violet\n• astra-light (planned)\n• dracula     (built-in via chroma)\n• github-dark (built-in via chroma)\n\nToggle with /theme astra-dark — full theme switching ships next.",
	}
}

func overlayFile(e *engine.Engine, path string) *overlay {
	safe, err := e.Perm.SafePath(path)
	if err != nil {
		return &overlay{title: "File: " + path, body: err.Error()}
	}
	data, err := readFileLimit(safe, 64*1024)
	if err != nil {
		return &overlay{title: "File: " + path, body: "(not readable)"}
	}
	rel := path
	preview := renderFilePreview(string(data), rel)
	return &overlay{title: "File: " + rel, body: preview}
}

func prettyJSON(v map[string]any) string {
	if len(v) == 0 {
		return "(empty)"
	}
	keys := make([]string, 0, len(v))
	for k := range v {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "  %s: %v\n", k, v[k])
	}
	return b.String()
}
