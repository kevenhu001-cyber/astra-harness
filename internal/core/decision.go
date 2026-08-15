package core

import (
	"math"
	"sort"
)

// Uncertainty is the complement of confidence: how much we don't know.
func (u *Unknown) Uncertainty() float64 {
	return clamp01(1 - u.Confidence)
}

// ComputePriority implements the v0.1 heuristic from the design document:
// priority = impact × uncertainty × dependency_weight ÷ resolution_cost.
func (u *Unknown) ComputePriority() float64 {
	cost := math.Max(0.05, clamp01(u.ResolutionCost))
	p := clamp01(u.Impact) * u.Uncertainty() * (1 + clamp01(u.DependencyWeight)) / cost
	u.Priority = math.Round(p*1000) / 1000
	return u.Priority
}

// RankUnknowns returns open unknowns sorted by computed priority.
func RankUnknowns(unknowns []*Unknown) []*Unknown {
	out := make([]*Unknown, 0, len(unknowns))
	for _, u := range unknowns {
		if u.Status != StatusResolved && u.Status != StatusCancelled {
			u.ComputePriority()
			out = append(out, u)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Priority == out[j].Priority {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].Priority > out[j].Priority
	})
	return out
}

// Default decision weights (v0.1 heuristics, tunable later).
const (
	GoalWeight     = 1.0
	InfoGainWeight = 0.8
	CostWeight     = 0.4
	RiskWeight     = 0.6
)

// ComputeUtility scores a candidate action:
// utility = goal_progress × goal_weight + info_gain × uncertainty_weight
//
//	− cost × cost_weight − risk × risk_weight.
func (a *Action) ComputeUtility() float64 {
	u := clamp01(a.ExpectedGoalProgress)*GoalWeight +
		clamp01(a.ExpectedInfoGain)*InfoGainWeight -
		clamp01(a.Cost)*CostWeight -
		clamp01(a.Risk)*RiskWeight
	a.Utility = math.Round(u*1000) / 1000
	return a.Utility
}

// NextBestAction chooses the highest-utility non-terminal action, or suggests
// a heuristic next step when no candidate action exists yet.
func NextBestAction(st *State, recentActions []*Action) *Action {
	var best *Action
	for _, a := range recentActions {
		if a.Status == StatusSucceeded || a.Status == StatusFailed || a.Status == StatusCancelled {
			continue
		}
		a.ComputeUtility()
		if best == nil || a.Utility > best.Utility {
			best = a
		}
	}
	if best != nil {
		return best
	}
	now := st.UpdatedAt
	_ = now
	// Heuristic fallbacks.
	for _, a := range recentActions {
		if a.Type == ActionEdit && a.Status == StatusSucceeded {
			suggestion := &Action{
				Type: ActionRunTest, Description: "Run tests to verify recent edits",
				ExpectedInfoGain: 0.7, ExpectedGoalProgress: 0.4, Cost: 0.2, Risk: 0.1,
			}
			suggestion.ComputeUtility()
			return suggestion
		}
	}
	unknowns := RankUnknowns(st.Unknowns)
	if len(unknowns) > 0 {
		u := unknowns[0]
		suggestion := &Action{
			Type: ActionSearch, Description: "Investigate top unknown: " + truncate(u.Description, 80),
			ExpectedInfoGain: u.Uncertainty() * u.Impact, ExpectedGoalProgress: 0.2, Cost: 0.1, Risk: 0.05,
		}
		suggestion.ComputeUtility()
		return suggestion
	}
	return nil
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
