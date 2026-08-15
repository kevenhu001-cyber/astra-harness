package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/kevenhu001-cyber/astra-harness/internal/core"
	"github.com/kevenhu001-cyber/astra-harness/internal/engine"
	"github.com/kevenhu001-cyber/astra-harness/internal/knowledge"
)

type overlay struct {
	title      string
	body       string
	items      []string
	sel        int
	onSelect   func(string)
	selectedID string
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
		if len(o.items) > 0 {
			o.sel = (o.sel - 1 + len(o.items)) % len(o.items)
		}
		return false, "", true
	case "down", "tab":
		if len(o.items) > 0 {
			o.sel = (o.sel + 1) % len(o.items)
		}
		return false, "", true
	case "enter":
		if len(o.items) > 0 && o.onSelect != nil {
			return true, o.items[o.sel], true
		}
		return true, "", true
	case "q":
		return true, "", true
	}
	return false, "", false
}

func (o *overlay) View(width, height int) string {
	var content strings.Builder
	content.WriteString(styleTitle.Render(o.title) + "\n\n")
	if len(o.items) > 0 {
		for i, it := range o.items {
			line := it
			if i == o.sel {
				line = styleTitle.Render("● " + it)
			} else {
				line = "  " + it
			}
			content.WriteString(line + "\n")
		}
		content.WriteString("\n" + styleDim.Render("↑↓ navigate · ⏎ select · esc close"))
	} else {
		content.WriteString(o.body)
		content.WriteString("\n\n" + styleDim.Render("esc close"))
	}
	maxW := width - 8
	if maxW > 110 {
		maxW = 110
	}
	box := styleOverlay.Width(maxW).Render(content.String())
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box)
}

// Overlay factories ----------------------------------------------------------

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
	var b strings.Builder
	if len(st.Claims) == 0 {
		b.WriteString("No claims yet. Claims are created from verification and evidence.")
	}
	for _, c := range st.Claims {
		fmt.Fprintf(&b, "[%s] %s %s %s  (conf %.0f%%, %d evidence)\n",
			c.Status, c.Subject, c.Predicate, c.Object, c.Confidence*100, len(c.EvidenceIDs))
	}
	return &overlay{title: "Claims", body: b.String()}
}

func overlayUnknowns(st *core.State) *overlay {
	var b strings.Builder
	unknowns := core.RankUnknowns(st.Unknowns)
	if len(unknowns) == 0 {
		b.WriteString("No open unknowns. Nice — but verify before declaring done.")
	}
	for _, u := range unknowns {
		fmt.Fprintf(&b, "[p=%.2f] %s\n   impact %.0f%% · uncertainty %.0f%% · cost %.0f%% · %s\n",
			u.Priority, u.Description, u.Impact*100, u.Uncertainty()*100, u.ResolutionCost*100, u.Status)
	}
	return &overlay{title: "Unknowns — ranked by priority", body: b.String()}
}

func overlayEvidence(st *core.State) *overlay {
	var b strings.Builder
	if len(st.Evidence) == 0 {
		b.WriteString("No evidence recorded yet.")
	}
	for _, ev := range st.Evidence {
		content := ev.Content
		if len(content) > 140 {
			content = content[:137] + "..."
		}
		fmt.Fprintf(&b, "[%s] %s · %s\n   %s\n", ev.Kind, ev.Source, ev.Status, strings.ReplaceAll(content, "\n", " "))
	}
	return &overlay{title: "Evidence", body: b.String()}
}

func overlayActions(st *core.State) *overlay {
	var b strings.Builder
	actions := append([]*core.Action(nil), st.Actions...)
	// newest first
	for i, j := 0, len(actions)-1; i < j; i, j = i+1, j-1 {
		actions[i], actions[j] = actions[j], actions[i]
	}
	if len(actions) == 0 {
		b.WriteString("No actions recorded.")
	}
	for i, a := range actions {
		if i >= 40 {
			break
		}
		fmt.Fprintf(&b, "[%s] %s · %s (u=%.2f)\n   %s\n",
			a.Status, a.Type, a.Description, a.Utility, strings.ReplaceAll(a.ResultSummary, "\n", " "))
	}
	return &overlay{title: "Actions", body: b.String()}
}

func overlayEvents(e *engine.Engine) *overlay {
	var b strings.Builder
	events := e.Store.State.Events
	if len(events) == 0 {
		b.WriteString("No events in the materialized state (see .astra/events.jsonl).")
	}
	start := 0
	if len(events) > 80 {
		start = len(events) - 80
	}
	for _, ev := range events[start:] {
		fmt.Fprintf(&b, "%s %s\n", ev.Timestamp.Format("15:04:05"), ev.Type)
	}
	return &overlay{title: "Events (materialized)", body: b.String()}
}

func overlaySessions(e *engine.Engine) *overlay {
	sessions, _ := e.Store.ListSessions()
	o := &overlay{title: "Sessions"}
	if len(sessions) == 0 {
		o.body = "No saved sessions."
		return o
	}
	for _, s := range sessions {
		o.items = append(o.items, fmt.Sprintf("%s · %s · %s · %d msgs", s.ID, s.Model, s.UpdatedAt.Format("2006-01-02 15:04"), len(s.Messages)))
	}
	o.onSelect = func(sel string) {
		id := strings.Fields(sel)[0]
		o.selectedID = id
	}
	return o
}

func overlayModels(e *engine.Engine) *overlay {
	o := &overlay{title: "Models — pick provider/model"}
	for _, p := range e.Router.Providers() {
		marker := " "
		if p.ID() == e.ProviderID() {
			marker = "●"
		}
		for _, m := range p.Models() {
			o.items = append(o.items, fmt.Sprintf("%s %s/%s", marker, p.ID(), m))
		}
	}
	o.onSelect = func(sel string) {
		parts := strings.Fields(sel)
		model := parts[len(parts)-1]
		provider := parts[len(parts)-2]
		o.selectedID = provider + "|" + model
	}
	return o
}

func overlayHelp() *overlay {
	body := `KEYS
  enter            send message
  alt+enter        insert newline
  ctrl+c           stop agent / quit when idle
  ctrl+up/down     prompt history
  ctrl+u / ctrl+d  scroll chat up/down
  pgup / pgdn      scroll chat
  tab / shift+tab  navigate slash command popup
  esc              close overlay / cancel

COMMANDS`
	for _, c := range slashCommands {
		body += fmt.Sprintf("\n  %-14s %s", c.Name, c.Desc)
	}
	return &overlay{title: "Help", body: body}
}

func overlayDiff(g *knowledge.Git) *overlay {
	return &overlay{title: "Git diff", body: g.Diff()}
}

func overlayDebug(e *engine.Engine) *overlay {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("root:        %s\n", e.Root))
	b.WriteString(fmt.Sprintf("provider:    %s\n", e.ProviderID()))
	b.WriteString(fmt.Sprintf("model:       %s\n", e.Model))
	b.WriteString(fmt.Sprintf("perm mode:   %s\n", e.Perm.GetMode()))
	b.WriteString(fmt.Sprintf("plan mode:   %v\n", e.Perm.IsPlanMode()))
	b.WriteString(fmt.Sprintf("state dir:   %s\n", e.StateDir()))
	b.WriteString(fmt.Sprintf("index:       %s\n", e.IndexPath()))
	b.WriteString(fmt.Sprintf("index stats: %s\n", e.Index.Stats()))
	b.WriteString(fmt.Sprintf("session:     %s\n", e.SessionID()))
	return &overlay{title: "Debug", body: b.String()}
}

func overlayCost(e *engine.Engine) *overlay {
	u := e.Usage()
	return &overlay{title: "Token usage",
		body: fmt.Sprintf("input tokens:  %d\noutput tokens: %d\ntotal:         %d",
			u.InputTokens, u.OutputTokens, u.InputTokens+u.OutputTokens)}
}

func overlayGoal(e *engine.Engine) *overlay {
	g := e.Store.ActiveGoal()
	if g == nil {
		return &overlay{title: "Goal", body: "No active goal. Type a task or use /goal <description>"}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "[%s] progress %.0f%%\n%s\n", g.Status, g.Progress*100, g.Description)
	if len(g.AcceptanceCriteria) > 0 {
		b.WriteString("\nAcceptance criteria:\n")
		for _, c := range g.AcceptanceCriteria {
			b.WriteString(" - " + c + "\n")
		}
	}
	return &overlay{title: "Active goal", body: b.String()}
}
