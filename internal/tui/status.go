package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// codexStatusCard renders the /status output as a Codex-style status card:
//
//	╭────────────────────────────────────────────╮
//	│  >_ Astra Harness (v0.1.0)                 │
//	│                                            │
//	│  Model:            gpt-4o                  │
//	│  Directory:        /path/to/project        │
//	│  Permissions:      ask                     │
//	│                                            │
//	│  Token usage:      1.05K total (700 in + … │
//	│  Context window:   [██████░░░░] 40% left   │
//	╰────────────────────────────────────────────╯
func codexStatusCard(a *app) string {
	e := a.engine
	usage := a.lastUsage
	total := usage.InputTokens + usage.OutputTokens
	cost := a.totalCost
	if cost <= 0 {
		cost = approximateCost(e.Model, usage)
	}
	model := e.Model
	if reasoning := e.ReasoningEffort(); reasoning != "" && reasoning != "medium" {
		model += " (reasoning " + reasoning + ")"
	}
	perm := e.Perm.GetMode()
	if e.Perm.IsPlanMode() {
		perm = "plan"
	}
	acct := a.userEmail
	if acct == "" {
		acct = "(signed out)"
	}
	branch := e.Git.BranchOr("")
	if branch == "" {
		branch = "-"
	}
	st := e.Store.State

	var rows []string
	rows = append(rows,
		statusRow("Model", model),
		statusRow("Provider", e.ProviderID()),
		statusRow("Directory", e.Root),
		statusRow("Permissions", perm),
		statusRow("Branch", branch),
		statusRow("Account", acct),
		statusRow("Session", e.SessionID()),
		"",
		statusRow("Token usage", fmt.Sprintf("%s total  (%s in + %s out)",
			formatTokensCompact(total),
			formatTokensCompact(usage.InputTokens),
			formatTokensCompact(usage.OutputTokens))),
	)
	if max := e.Config.MaxContextTokens; max > 0 {
		usedPct := 0
		if total > 0 {
			usedPct = total * 100 / max
			if usedPct > 100 {
				usedPct = 100
			}
		}
		left := 100 - usedPct
		rows = append(rows, statusRow("Context window",
			fmt.Sprintf("%s %d%% left (%s used / %s)",
				statusProgressBar(left, 18), left,
				formatTokensCompact(total), formatTokensCompact(max))))
	}
	if cost > 0 {
		rows = append(rows, statusRow("Estimated cost", fmt.Sprintf("$%.4f", cost)))
	}
	rows = append(rows,
		"",
		statusRow("Knowledge", fmt.Sprintf("goals=%d claims=%d evidence=%d unknowns=%d actions=%d",
			len(st.Goals), len(st.Claims), len(st.Evidence), len(st.Unknowns), len(st.Actions))),
		statusRow("Session stats", fmt.Sprintf("%d turns · %d ok · %d failed",
			a.turns, a.successCnt, a.failCnt)),
	)

	var b strings.Builder
	b.WriteString("  " + styleTitle.Render(">_ Astra Harness (v0.1.0)") + "\n\n")
	for _, r := range rows {
		if r == "" {
			b.WriteString("\n")
			continue
		}
		b.WriteString("  " + r + "\n")
	}
	pal := activePalette()
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(pal.GrayLo).
		Padding(0, 1).
		Render(strings.TrimRight(b.String(), "\n"))
}

// statusRow renders a "Label: value" pair with Codex's aligned label column.
func statusRow(label, value string) string {
	return styleValue.Render(padRight(label+":", 18)) + styleBody.Render(value)
}

// statusProgressBar renders Codex's context-window bar: filled blocks and
// dim empty blocks inside brackets.
func statusProgressBar(pct, width int) string {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	filled := pct * width / 100
	return styleDim.Render("[") +
		styleKey.Render(strings.Repeat("█", filled)) +
		styleDim.Render(strings.Repeat("░", width-filled)) +
		styleDim.Render("]")
}

// formatTokensCompact formats token counts Codex-style: 1.05K, 2.3M, 412.
func formatTokensCompact(n int) string {
	switch {
	case n < 1000:
		return fmt.Sprintf("%d", n)
	case n < 1_000_000:
		return fmt.Sprintf("%.1fK", float64(n)/1000)
	default:
		return fmt.Sprintf("%.2fM", float64(n)/1_000_000)
	}
}
