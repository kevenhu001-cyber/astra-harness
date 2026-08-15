package core

import (
	"fmt"
	"strings"
	"time"
)

// Compiler turns raw state into a compact, decision-ready context summary.
// Context is a State Query result, not chat history accumulation.
type Compiler struct {
	MaxUnknowns int
	MaxActions  int
}

func NewCompiler() *Compiler {
	return &Compiler{MaxUnknowns: 5, MaxActions: 6}
}

// Compile produces a P0/P1-priority context block used in the system prompt.
func (c *Compiler) Compile(st *State, recentEvents []string) string {
	var b strings.Builder
	if st.Project != nil {
		fmt.Fprintf(&b, "PROJECT: %s\n", st.Project.Name)
		if st.Project.DefaultBranch != "" {
			fmt.Fprintf(&b, "DEFAULT BRANCH: %s\n", st.Project.DefaultBranch)
		}
		if len(st.Project.Languages) > 0 {
			pairs := make([]string, 0, len(st.Project.Languages))
			for lang, count := range st.Project.Languages {
				pairs = append(pairs, fmt.Sprintf("%s (%d files)", lang, count))
			}
			b.WriteString("LANGUAGES: " + strings.Join(pairs, ", ") + "\n")
		}
	}

	if g := c.activeGoal(st); g != nil {
		fmt.Fprintf(&b, "\nACTIVE GOAL [%s, progress %.0f%%]: %s\n", g.Status, g.Progress*100, g.Description)
		if len(g.AcceptanceCriteria) > 0 {
			b.WriteString("ACCEPTANCE CRITERIA:\n")
			for _, cr := range g.AcceptanceCriteria {
				fmt.Fprintf(&b, "- %s\n", cr)
			}
		}
	}

	claims := st.Claims
	if len(claims) > 0 {
		var verified, supported, contradicted int
		for _, cl := range claims {
			switch cl.Status {
			case ClaimVerified:
				verified++
			case ClaimSupported:
				supported++
			case ClaimContradicted:
				contradicted++
			}
		}
		fmt.Fprintf(&b, "\nCLAIMS: %d total | %d verified | %d supported | %d contradicted\n", len(claims), verified, supported, contradicted)
		for _, cl := range claims {
			if cl.Status == ClaimVerified || cl.Status == ClaimContradicted {
				fmt.Fprintf(&b, "- [%s] %s %s %s (confidence %.0f%%)\n", cl.Status, cl.Subject, cl.Predicate, cl.Object, cl.Confidence*100)
			}
		}
	}

	unknowns := RankUnknowns(st.Unknowns)
	if len(unknowns) > 0 {
		fmt.Fprintf(&b, "\nTOP UNKNOWNS (priority = impact × uncertainty × dependency ÷ cost):\n")
		for i, u := range unknowns {
			if i >= c.MaxUnknowns {
				break
			}
			fmt.Fprintf(&b, "- [p=%.2f] %s (impact %.0f%%, uncertainty %.0f%%, cost %.0f%%)\n",
				u.Priority, u.Description, u.Impact*100, u.Uncertainty()*100, u.ResolutionCost*100)
		}
	}

	if len(recentEvents) > 0 {
		fmt.Fprintf(&b, "\nRECENT EVENTS (%d):\n", len(recentEvents))
		for _, ev := range recentEvents {
			b.WriteString("- " + ev + "\n")
		}
	}

	if nb := NextBestAction(st, st.Actions); nb != nil {
		fmt.Fprintf(&b, "\nNEXT BEST ACTION: %s (%s)\n", nb.Type, nb.Description)
	}

	if st.UpdatedAt.IsZero() {
		st.UpdatedAt = time.Now().UTC()
	}
	fmt.Fprintf(&b, "\nSTATE UPDATED: %s\n", st.UpdatedAt.Format(time.RFC3339))
	return b.String()
}

func (c *Compiler) activeGoal(st *State) *Goal {
	var best *Goal
	for _, g := range st.Goals {
		if g.Status != StatusActive {
			continue
		}
		if best == nil || g.Priority > best.Priority || g.UpdatedAt.After(best.UpdatedAt) {
			best = g
		}
	}
	return best
}

// DescribeAction renders a human-readable action for logs and events.
func DescribeAction(a *Action) string {
	if a.Description != "" {
		return a.Description
	}
	return a.Type
}
