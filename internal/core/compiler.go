package core

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Compiler turns raw state into a compact, decision-ready context summary.
// Context is a State Query result, not chat history accumulation.
type Compiler struct {
	MaxUnknowns int
	MaxActions  int
	MaxClaims   int
}

func NewCompiler() *Compiler {
	return &Compiler{MaxUnknowns: 5, MaxActions: 6, MaxClaims: 8}
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
		// Cross-session memory activation: rank claims by relevance to the
		// active goal (term overlap), then confidence, then recency, and cap
		// the listing so state accumulation cannot bloat the context.
		goalDesc := ""
		if g := c.activeGoal(st); g != nil {
			goalDesc = g.Description
		}
		ranked := RankClaimsByGoal(claims, goalDesc)
		shown := 0
		eligible := 0
		for _, cl := range ranked {
			if cl.Status != ClaimVerified && cl.Status != ClaimContradicted {
				continue
			}
			eligible++
			if shown >= c.MaxClaims {
				continue
			}
			shown++
			fmt.Fprintf(&b, "- [%s] %s %s %s (confidence %.0f%%)\n", cl.Status, cl.Subject, cl.Predicate, cl.Object, cl.Confidence*100)
		}
		if shown < eligible {
			fmt.Fprintf(&b, "… %d more claim(s)\n", eligible-shown)
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

// RankClaimsByGoal orders claims by relevance to the goal description (token
// overlap across subject/predicate/object), then confidence, then recency.
// This is the cross-session memory activation step: prior sessions' verified
// conclusions surface first when they concern the current task.
func RankClaimsByGoal(claims []*Claim, goalDesc string) []*Claim {
	terms := goalTerms(goalDesc)
	out := append([]*Claim(nil), claims...)
	sort.SliceStable(out, func(i, j int) bool {
		si, sj := claimScore(out[i], terms), claimScore(out[j], terms)
		if si == sj {
			return out[i].UpdatedAt.After(out[j].UpdatedAt)
		}
		return si > sj
	})
	return out
}

func claimScore(cl *Claim, terms []string) float64 {
	score := cl.Confidence * 0.3
	if cl.Status == ClaimVerified {
		score += 0.5 // verified conclusions outrank contradicted ones
	}
	text := strings.ToLower(cl.Subject + " " + cl.Predicate + " " + cl.Object)
	for _, t := range terms {
		if strings.Contains(text, t) {
			score += 1
		}
	}
	score += recencyScore(cl.UpdatedAt) * 0.2
	return score
}

func recencyScore(t time.Time) float64 {
	days := time.Since(t).Hours() / 24
	if days >= 30 {
		return 0
	}
	return 1 - days/30
}

func goalTerms(s string) []string {
	var out []string
	for _, f := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_')
	}) {
		if len(f) >= 3 {
			out = append(out, f)
		}
	}
	return out
}

// DescribeAction renders a human-readable action for logs and events.
func DescribeAction(a *Action) string {
	if a.Description != "" {
		return a.Description
	}
	return a.Type
}
