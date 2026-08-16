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

// overlay is a generic centered overlay with a left list + right detail. Tabs
// partition the items/detail slices into ranges — navigation (j/k) is
// scoped to the active tab.
type overlay struct {
	title    string
	footer   string
	items    []string
	detail   []string
	body     string
	tabs     []string
	tabEnds  []int // cumulative length after each tab; len(tabs) buckets
	tab      int
	sel      int
	onSelect func(string) string
}

func (o *overlay) empty() bool { return len(o.items) == 0 && o.body == "" }

// tabRange returns [start, end) indices for the current tab.
func (o *overlay) tabRange() (int, int) {
	if len(o.tabEnds) == 0 {
		return 0, len(o.items)
	}
	if o.tab < 0 || o.tab >= len(o.tabEnds) {
		return 0, len(o.items)
	}
	end := o.tabEnds[o.tab]
	start := 0
	if o.tab > 0 {
		start = o.tabEnds[o.tab-1]
	}
	return start, end
}

// append adds an item + detail pair to the current tab's bucket.
func (o *overlay) append(label, detail string) {
	if len(o.tabEnds) == 0 {
		o.tabEnds = append(o.tabEnds, 0)
	}
	o.tabEnds[len(o.tabEnds)-1]++
	o.items = append(o.items, label)
	o.detail = append(o.detail, detail)
}

// finishTab terminates the current bucket and starts a fresh one.
func (o *overlay) finishTab() {
	o.tabEnds = append(o.tabEnds, len(o.items))
}

// clampSel keeps the cursor inside the active tab range.
func (o *overlay) clampSel() {
	start, end := o.tabRange()
	if end-start == 0 {
		o.sel = 0
		return
	}
	if o.sel < start {
		o.sel = start
	}
	if o.sel >= end {
		o.sel = end - 1
	}
}

func (o *overlay) update(msg tea.Msg) (closed bool, selected string, handled bool) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return false, "", false
	}
	switch key.String() {
	case "esc", "ctrl+c":
		return true, "", true
	case "up", "shift+tab":
		start, end := o.tabRange()
		if end > start {
			if o.sel <= start {
				o.sel = end - 1
			} else {
				o.sel--
			}
		}
		return false, "", true
	case "down", "tab":
		start, end := o.tabRange()
		if end > start {
			if o.sel >= end-1 {
				o.sel = start
			} else {
				o.sel++
			}
		}
		return false, "", true
	case "left":
		if len(o.tabs) > 0 {
			o.tab = (o.tab - 1 + len(o.tabs)) % len(o.tabs)
			o.clampSel()
		}
		return false, "", true
	case "right":
		if len(o.tabs) > 0 {
			o.tab = (o.tab + 1) % len(o.tabs)
			o.clampSel()
		}
		return false, "", true
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		idx := int(key.String()[0] - '1')
		if idx < len(o.tabs) {
			o.tab = idx
			o.clampSel()
		}
		return false, "", true
	case "enter":
		if len(o.items) > 0 && o.sel < len(o.items) && o.onSelect != nil {
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
	if o.body != "" && len(o.items) == 0 && o.tab == 0 {
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
	leftW := maxW/2 - 2
	if leftW < 28 {
		leftW = 28
	}
	rightW := maxW - leftW - 6

	var tabs strings.Builder
	if len(o.tabs) > 0 {
		for i, t := range o.tabs {
			if i == o.tab {
				tabs.WriteString(styleKey.Render(" " + t + " "))
			} else {
				tabs.WriteString(styleDim.Render("  " + t + "  "))
			}
			tabs.WriteString(styleDim.Render("  "))
		}
	}

	var b strings.Builder
	b.WriteString(styleTitle.Render("◆ " + o.title))
	b.WriteString("\n")
	if tabs.Len() > 0 {
		b.WriteString(tabs.String())
		b.WriteString("\n")
	}
	b.WriteString(styleFaint.Render(strings.Repeat("─", maxW-4)))
	b.WriteString("\n\n")

	left := renderList(o, leftW)
	right := renderDetail(o, rightW)

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
		b.WriteString(styleDim.Render("↑↓ select · ←→ tab · ⏎ open · esc close"))
	}
	box := styleOverlay.Width(maxW).Render(b.String())
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box)
}

func renderList(o *overlay, w int) string {
	var b strings.Builder
	start, end := o.tabRange()
	maxRows := 18
	for i := start; i < end; i++ {
		if i-start >= maxRows {
			b.WriteString(styleDim.Render(fmt.Sprintf("  …+%d more", end-i)))
			break
		}
		text := truncate(o.items[i], w-4)
		if i == o.sel {
			b.WriteString(styleTitle.Render("● " + text))
		} else {
			b.WriteString(styleBody.Render("  " + text))
		}
		b.WriteString("\n")
	}
	if end-start == 0 {
		b.WriteString(styleDim.Render("(no items)"))
	}
	return b.String()
}

func renderDetail(o *overlay, w int) string {
	if o.sel < 0 || o.sel >= len(o.detail) {
		return styleDim.Render("No detail")
	}
	if o.detail[o.sel] == "" {
		return styleDim.Render("No detail")
	}
	return o.detail[o.sel]
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
		label  string
		order  int
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
		o.append(r.label, r.detail)
	}
	o.finishTab()
	// "By confidence" bucket: same items sorted differently.
	byConf := append([]row(nil), rows...)
	sort.SliceStable(byConf, func(i, j int) bool { return byConf[i].label < byConf[j].label })
	for _, r := range byConf {
		o.append(r.label, r.detail)
	}
	o.finishTab()
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
	o := &overlay{title: "Unknowns — ranked by priority", tabs: []string{"Ranked", "All"}}
	for _, u := range unknowns {
		o.append(fmt.Sprintf("p=%.2f  %s", u.Priority, truncate(u.Description, 60)), formatUnknownDetail(u))
	}
	o.finishTab()
	for _, u := range unknowns {
		o.append(truncate(u.Description, 60), formatUnknownDetail(u))
	}
	o.finishTab()
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
	bucket := func(filter string) {
		for _, ev := range st.Evidence {
			if filter != "" && ev.Kind != filter {
				continue
			}
			label := fmt.Sprintf("[%s] %.0f%%  %s", ev.Kind, ev.Confidence*100, truncate(ev.Source, 50))
			o.append(label, formatEvidenceDetail(ev))
		}
		o.finishTab()
	}
	bucket("")
	bucket(core.EvidenceTestResult)
	bucket(core.EvidenceBuildResult)
	bucket(core.EvidenceRuntimeResult)
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
		o.append(fmt.Sprintf("[%s] %s  u=%.2f", a.Status, truncate(a.Description, 60), a.Utility), formatActionDetail(a))
	}
	o.finishTab()
	byUtil := append([]*core.Action(nil), actions...)
	sort.SliceStable(byUtil, func(i, j int) bool { return byUtil[i].Utility > byUtil[j].Utility })
	for _, a := range byUtil {
		o.append(fmt.Sprintf("[%s] u=%.2f  %s", a.Status, a.Utility, truncate(a.Description, 60)), formatActionDetail(a))
	}
	o.finishTab()
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
		return &overlay{title: "Event log", body: "No events yet."}
	}
	o := &overlay{title: "Event log", tabs: []string{"Recent", "All"}}
	start := 0
	if len(events) > 80 {
		start = len(events) - 80
	}
	for _, ev := range events[start:] {
		label := fmt.Sprintf("%s  %s", ev.Timestamp.Format("15:04:05.000"), ev.Type)
		detail := fmt.Sprintf("Type: %s\nID:   %s\nTime: %s\n\nData:\n%s",
			ev.Type, ev.ID, ev.Timestamp.Format(time.RFC3339), prettyJSON(ev.Data))
		o.append(label, detail)
	}
	o.finishTab()
	for _, ev := range events {
		label := fmt.Sprintf("%s  %s", ev.Timestamp.Format("15:04:05.000"), ev.Type)
		detail := fmt.Sprintf("Type: %s\nID:   %s\nTime: %s\n\nData:\n%s",
			ev.Type, ev.ID, ev.Timestamp.Format(time.RFC3339), prettyJSON(ev.Data))
		o.append(label, detail)
	}
	o.finishTab()
	return o
}

func overlaySessions(e *engine.Engine) *overlay {
	sessions, _ := e.Store.ListSessions()
	if len(sessions) == 0 {
		return &overlay{title: "Sessions", body: "No saved sessions. Run a task to create one."}
	}
	o := &overlay{title: "Sessions", tabs: []string{"Recent", "By model", "By size"}}
	cur := e.SessionID()
	for _, s := range sessions {
		marker := " "
		if s.ID == cur {
			marker = "●"
		}
		label := fmt.Sprintf("%s %s · %d msgs · %s",
			marker, truncate(s.ID, 18), len(s.Messages), s.UpdatedAt.Format("01-02 15:04"))
		detail := fmt.Sprintf("ID:        %s\nCreated:   %s\nUpdated:   %s\nModel:     %s\nMessages:  %d\nGoal:      %s",
			s.ID, s.CreatedAt.Format(time.RFC3339), s.UpdatedAt.Format(time.RFC3339),
			s.Model, len(s.Messages), s.GoalID)
		o.append(label, detail)
	}
	o.finishTab()
	byModel := append([]*core.Session(nil), sessions...)
	sort.SliceStable(byModel, func(i, j int) bool { return byModel[i].Model < byModel[j].Model })
	for _, s := range byModel {
		o.append(fmt.Sprintf("%s · %s", truncate(s.ID, 18), s.Model),
			fmt.Sprintf("ID: %s\nModel: %s\nMessages: %d", s.ID, s.Model, len(s.Messages)))
	}
	o.finishTab()
	bySize := append([]*core.Session(nil), sessions...)
	sort.SliceStable(bySize, func(i, j int) bool { return len(bySize[i].Messages) > len(bySize[j].Messages) })
	for _, s := range bySize {
		o.append(fmt.Sprintf("%s · %d msgs", truncate(s.ID, 18), len(s.Messages)),
			fmt.Sprintf("ID: %s\nModel: %s\nMessages: %d", s.ID, s.Model, len(s.Messages)))
	}
	o.finishTab()
	o.onSelect = func(sel string) string {
		id := strings.Fields(sel)
		if len(id) == 0 {
			return ""
		}
		// Strip the leading "●"/" " marker.
		if id[0] == "●" || id[0] == "○" {
			if len(id) > 1 {
				return id[1]
			}
			return ""
		}
		return id[0]
	}
	return o
}

func overlayProviderConfig(e *engine.Engine) *overlay {
	var b strings.Builder
	for _, p := range e.Config.Providers {
		keyState := p.APIKeyEnv
		if p.APIKey != "" {
			keyState = "set (stored in .astra/config.json)"
		}
		if keyState == "" {
			keyState = "unset"
		}
		url := p.BaseURL
		if url == "" {
			url = "—"
		}
		models := strings.Join(p.Models, ", ")
		if models == "" {
			models = "—"
		}
		b.WriteString(styleTitle.Render("["+p.ID+"]") + "\n")
		b.WriteString(fmt.Sprintf("  type:    %s\n", p.Type))
		b.WriteString(fmt.Sprintf("  url:     %s\n", url))
		b.WriteString(fmt.Sprintf("  api key: %s\n", keyState))
		b.WriteString(fmt.Sprintf("  models:  %s\n", models))
		b.WriteString("\n")
	}
	b.WriteString(styleDim.Render("/set-url <provider> <url> · /set-key <provider> <key> · /set-model <provider> <model>"))
	return &overlay{title: "Provider configuration", body: b.String(), footer: "esc close"}
}

func overlayModels(e *engine.Engine) *overlay {
	recent := e.RecentModels()
	o := &overlay{title: "Models — pick provider/model", tabs: []string{"Recent", "Available", "Configured"}}
	for _, id := range recent {
		parts := strings.SplitN(id, "|", 2)
		desc := "recently used"
		if len(parts) == 2 {
			desc = describePricing(parts[1])
		}
		o.append("★ "+id, fmt.Sprintf("Recent pick:\n  provider: %s\n  model:    %s\n  pricing:  %s",
			safePick(parts, 0), safePick(parts, 1), desc))
	}
	if len(recent) == 0 {
		o.append("(no recent — switch a model to populate)",
			"Recent models will appear here once you switch via /model.")
	}
	o.finishTab()
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
			label := fmt.Sprintf("%s %s/%s  %s", marker, p.ID(), m, avail)
			defaultMark := ""
			if p.DefaultModel() == m {
				defaultMark = " (default)"
			}
			o.append(label, fmt.Sprintf("Provider:  %s\nID:        %s\nModel:     %s%s\nAvailable: %v\nPricing:   %s\n\nPricing reflects USD per million tokens. Local models (Ollama) are 0.",
				p.Name(), p.ID(), m, defaultMark, p.Available(), describePricing(m)))
		}
	}
	o.finishTab()
	for _, p := range e.Router.Providers() {
		for _, m := range p.Models() {
			o.append(fmt.Sprintf("%s/%s", p.ID(), m),
				fmt.Sprintf("%s\n%s\n(no availability check)", p.Name(), m))
		}
	}
	o.finishTab()
	o.onSelect = func(sel string) string {
		text := strings.TrimSpace(strings.TrimPrefix(sel, "★"))
		if strings.HasPrefix(text, "○ ") || strings.HasPrefix(text, "● ") {
			text = strings.TrimSpace(text[2:])
		}
		if idx := strings.Index(text, "  "); idx > 0 {
			text = text[:idx]
		}
		parts := strings.SplitN(text, "/", 2)
		if len(parts) != 2 {
			return ""
		}
		return parts[0] + "|" + parts[1]
	}
	return o
}

func safePick(parts []string, i int) string {
	if i < len(parts) {
		return parts[i]
	}
	return ""
}

func describePricing(model string) string {
	p := pricingFor(model)
	if p.Input == 0 && p.Output == 0 {
		return "free / local"
	}
	return fmt.Sprintf("in $%.2f / out $%.2f", p.Input, p.Output)
}

func overlayHelp() *overlay {
	o := &overlay{title: "Help", tabs: append([]string{}, slashCategories...)}
	byCat := map[string][]slashCmd{}
	for _, c := range slashCommands {
		cat := c.Category
		if cat == "" {
			cat = "Help"
		}
		byCat[cat] = append(byCat[cat], c)
	}
	emit := func(c slashCmd) {
		shortcut := ""
		if c.Shortcut != "" {
			shortcut = "  (" + c.Shortcut + ")"
		}
		o.append(fmt.Sprintf("%-14s  %s", c.Name, c.Desc),
			fmt.Sprintf("Command:     %s\nCategory:    %s\nDescription: %s%s\n\nUsage: type / then press Tab to autocomplete.",
				c.Name, c.Category, c.Desc, shortcut))
	}
	for _, cat := range slashCategories {
		for _, cmd := range byCat[cat] {
			emit(cmd)
		}
		o.finishTab()
	}
	return o
}

// overlayShortcuts mirrors Codex's "? for shortcuts" overlay: the same
// keybinding rows and wording, with the Astra product name kept in place.
func overlayShortcuts() *overlay {
	body := strings.Join([]string{
		"/  for commands",
		"!  for shell commands",
		"ctrl+j  for newline",
		"tab  to submit message",
		"@  for file paths",
		"ctrl+v  to paste images",
		"ctrl+g  to edit in external editor",
		"esc esc  to edit previous message",
		"ctrl+r  search history",
		"ctrl+c  to exit",
		"ctrl+t  to view transcript",
		"alt+,  reasoning down",
		"alt+.  reasoning up",
		"",
		"customize shortcuts with /keymap",
	}, "\n")
	return &overlay{title: "Shortcuts", body: body, footer: "esc close"}
}

func overlayTranscript(a *app) *overlay {
	var b strings.Builder
	for _, it := range a.items {
		switch it.kind {
		case "user":
			fmt.Fprintf(&b, "> %s\n\n", it.raw)
		case "assistant":
			b.WriteString(it.raw + "\n\n")
		case "system":
			b.WriteString(it.raw + "\n\n")
		case "tool":
			fmt.Fprintf(&b, "[tool] %s\n%s\n\n", it.meta, it.raw)
		default:
			b.WriteString(it.raw + "\n\n")
		}
	}
	if b.Len() == 0 {
		b.WriteString("(empty transcript)")
	}
	return &overlay{title: "Transcript", body: b.String(), footer: "esc close"}
}

func overlayDiff(g *knowledge.Git) *overlay {
	body := g.Diff()
	if body == "" {
		body = "(working tree clean)"
	}
	return &overlay{title: "Git diff · unstaged", body: body, tabs: []string{"Unstaged"}, tabEnds: []int{1}}
}

func overlayDiffBase(g *knowledge.Git, base string) *overlay {
	body := g.DiffBase(base)
	if body == "" {
		body = fmt.Sprintf("(no diff against %s)", base)
	}
	return &overlay{title: "Git diff · vs " + base, body: body, tabs: []string{base}, tabEnds: []int{1}}
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
	fmt.Fprintf(&b, "reasoning:   %s\n", e.ReasoningEffort())
	fmt.Fprintf(&b, "theme:       %s\n", CurrentTheme())
	return &overlay{title: "Debug info", body: b.String()}
}

func overlayCost(e *engine.Engine) *overlay {
	u := e.Usage()
	cost := approximateCost(e.Model, u)
	pr := pricingFor(e.Model)
	var b strings.Builder
	fmt.Fprintf(&b, "Model:           %s\nProvider:        %s\n", e.Model, e.ProviderID())
	fmt.Fprintf(&b, "Input tokens:    %d\nOutput tokens:   %d\nTotal tokens:    %d\n", u.InputTokens, u.OutputTokens, u.InputTokens+u.OutputTokens)
	if u.CacheReadTokens > 0 || u.ReasoningTokens > 0 {
		fmt.Fprintf(&b, "Cache read:      %d\nReasoning:       %d\n", u.CacheReadTokens, u.ReasoningTokens)
	}
	fmt.Fprintf(&b, "\nPricing (per 1M tok):\n  input:   $%.2f\n  output:  $%.2f\n", pr.Input, pr.Output)
	fmt.Fprintf(&b, "\nEstimated cost:  $%.4f\n", cost)
	return &overlay{title: "Token usage & cost", body: b.String()}
}

func overlayStats(a *app) *overlay {
	sessions, _ := a.engine.Store.ListSessions()
	rate := 1.0
	if n := a.successCnt + a.failCnt; n > 0 {
		rate = float64(a.successCnt) / float64(n)
	}
	u := a.engine.Usage()
	elapsed := time.Since(a.startedAt).Truncate(time.Second)
	var b strings.Builder
	fmt.Fprintf(&b, "Session:\n")
	fmt.Fprintf(&b, "  id             %s\n", a.engine.SessionID())
	fmt.Fprintf(&b, "  elapsed        %s\n", elapsed)
	fmt.Fprintf(&b, "  turns          %d\n", a.turns)
	fmt.Fprintf(&b, "  tool calls     %d (%d ok / %d failed)\n", a.successCnt+a.failCnt, a.successCnt, a.failCnt)
	fmt.Fprintf(&b, "  tool success   %.0f%%\n", rate*100)
	fmt.Fprintf(&b, "\nTokens (last turn):\n")
	fmt.Fprintf(&b, "  input / output %d → %d\n", u.InputTokens, u.OutputTokens)
	if u.CacheReadTokens > 0 || u.ReasoningTokens > 0 {
		fmt.Fprintf(&b, "  cache / reason %d / %d\n", u.CacheReadTokens, u.ReasoningTokens)
	}
	fmt.Fprintf(&b, "\nTokens (cumulative):\n")
	fmt.Fprintf(&b, "  total          %d\n", a.totalTokens)
	fmt.Fprintf(&b, "  cost           $%.4f\n", a.totalCost)
	fmt.Fprintf(&b, "\nKnowledge:\n")
	fmt.Fprintf(&b, "  claims         %d\n", len(a.engine.Store.State.Claims))
	fmt.Fprintf(&b, "  unknowns       %d\n", len(a.engine.Store.State.Unknowns))
	fmt.Fprintf(&b, "  evidence       %d\n", len(a.engine.Store.State.Evidence))
	fmt.Fprintf(&b, "  actions        %d\n", len(a.engine.Store.State.Actions))
	fmt.Fprintf(&b, "\nSessions on disk:  %d\n", len(sessions))
	return &overlay{title: "Session stats", body: b.String()}
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

func overlayMcp(e *engine.Engine) *overlay {
	configured := e.Config.McpServers
	connected := e.McpToolNames()
	if len(configured) == 0 {
		return &overlay{
			title: "MCP servers",
			body:  "No MCP servers configured.\n\nAstra supports the Model Context Protocol over stdio; configured\nservers' tools are exposed to the model as mcp__<server>__<tool>.\n\nAdd servers to .astra/config.json:\n\n{\n  \"mcp_servers\": [\n    {\n      \"id\": \"github\",\n      \"command\": \"mcp-github\",\n      \"args\": []\n    }\n  ]\n}\n\nThen restart Astra (or /init to reconnect).",
		}
	}
	var b strings.Builder
	for _, sc := range configured {
		status := styleError.Render("✗ not connected")
		for _, t := range connected {
			if strings.HasPrefix(t, sc.ID+"/") {
				status = styleOk.Render("✓ connected")
				break
			}
		}
		kind := sc.Type
		if kind == "" {
			kind = "stdio"
		}
		fmt.Fprintf(&b, "%s  %s  [%s]\n", sc.ID, status, kind)
		if kind == "http" {
			fmt.Fprintf(&b, "  url: %s\n", sc.URL)
		} else {
			fmt.Fprintf(&b, "  command: %s %s\n", sc.Command, strings.Join(sc.Args, " "))
		}
		disabled := 0
		for _, tc := range sc.Tools {
			if tc.Disabled {
				disabled++
			}
		}
		if disabled > 0 {
			fmt.Fprintf(&b, "  disabled tools: %d\n", disabled)
		}
	}
	if len(connected) > 0 {
		b.WriteString("\n" + styleTitle.Render("Exposed tools") + "\n")
		for _, t := range connected {
			fmt.Fprintf(&b, "  mcp__%s\n", t)
		}
	}
	b.WriteString("\nMCP tool calls are gated by the EXECUTE permission; disabled tools\nare not exposed and dispatch is refused.")
	return &overlay{title: "MCP servers", body: b.String()}
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
		body:  "Sub-agents are spawned from the main loop when an action requires\nisolation or a different model.\n\nFuture:\n  /spawn <task>  — create a sub-agent in its own session\n  /agents        — list active sub-agents\n  /kill <id>     — terminate a sub-agent\n\n(Implemented in Phase 11/12.)",
	}
}

func overlayThemes() *overlay {
	names := ThemeNames()
	var b strings.Builder
	current := CurrentTheme()
	fmt.Fprintf(&b, "current: %s\n\nregistered themes:\n", current)
	for _, n := range names {
		marker := "  "
		if n == current {
			marker = "● "
		}
		fmt.Fprintf(&b, "  %s%s\n", marker, n)
	}
	b.WriteString("\nUsage:\n  /theme list           show this overlay\n  /theme <name>         switch theme (astra-dark / astra-light / mono)")
	return &overlay{title: "Themes", body: b.String()}
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
