package core

import (
	"math"
	"strings"
	"testing"
	"time"
)

func TestUnknownPriority(t *testing.T) {
	u := &Unknown{
		Impact: 0.8, Confidence: 0.3, ResolutionCost: 0.2, DependencyWeight: 0.4,
	}
	got := u.ComputePriority()
	want := 0.8 * 0.7 * 1.4 / 0.2
	if math.Abs(got-want) > 0.001 {
		t.Fatalf("priority = %.3f, want %.3f", got, want)
	}
}

func TestUnknownUncertainty(t *testing.T) {
	u := &Unknown{Confidence: 0.25}
	if u.Uncertainty() != 0.75 {
		t.Fatalf("uncertainty = %v, want 0.75", u.Uncertainty())
	}
}

func TestActionUtility(t *testing.T) {
	a := &Action{
		ExpectedGoalProgress: 0.5, ExpectedInfoGain: 0.4, Cost: 0.2, Risk: 0.3,
	}
	got := a.ComputeUtility()
	want := 0.5*GoalWeight + 0.4*InfoGainWeight - 0.2*CostWeight - 0.3*RiskWeight
	if math.Abs(got-want) > 0.001 {
		t.Fatalf("utility = %.3f, want %.3f", got, want)
	}
}

func TestStoreEventReplay(t *testing.T) {
	root := t.TempDir()
	s, err := NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	g := &Goal{
		ID: NewID("goal"), Description: "make it work", Status: StatusActive,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := s.AddGoal(g); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	got := s2.ActiveGoal()
	if got == nil || got.ID != g.ID {
		t.Fatalf("replayed goal mismatch: %+v", got)
	}
	if len(s2.State.Events) != 1 {
		t.Fatalf("expected 1 materialized event, got %d", len(s2.State.Events))
	}
}

func TestCompilerIncludesUnknowns(t *testing.T) {
	st := &State{
		Project: &Project{Name: "demo", Root: "/tmp/demo"},
		Goals:   []*Goal{{ID: "g1", Description: "goal", Status: StatusActive}},
		Unknowns: []*Unknown{{
			ID: "u1", Description: "transaction semantics unknown", Impact: 0.8,
			Confidence: 0.2, ResolutionCost: 0.4, DependencyWeight: 0.3,
		}},
	}
	out := NewCompiler().Compile(st, nil)
	if !strings.Contains(out, "transaction semantics unknown") {
		t.Fatalf("compiler output missing unknown:\n%s", out)
	}
	if !strings.Contains(out, "TOP UNKNOWNS") {
		t.Fatalf("compiler output missing unknowns section:\n%s", out)
	}
}
