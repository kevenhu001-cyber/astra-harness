package core

import (
	"fmt"
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

func TestReconcileState(t *testing.T) {
	st := &State{
		Evidence: []*Evidence{
			{ID: "ev1", CodeState: "A", Status: "VALID"},
			{ID: "ev2", CodeState: "B", Status: "VALID"},
			{ID: "ev3", CodeState: "A", Status: "STALE"}, // already stale
		},
		Claims: []*Claim{
			{ID: "c1", Status: ClaimVerified, CodeState: "A", EvidenceIDs: []string{"ev1"}},
			{ID: "c2", Status: ClaimVerified, CodeState: "B", EvidenceIDs: []string{"ev2"}},
			{ID: "c3", Status: ClaimSupported, CodeState: "A"},
			{ID: "c4", Status: ClaimHypothesis, CodeState: "A"}, // not verified: untouched
			{ID: "c5", Status: ClaimVerified, EvidenceIDs: []string{"ev2"}}, // no own state, fresh evidence
			{ID: "c6", Status: ClaimVerified, CodeState: "B", EvidenceIDs: []string{"ev1"}}, // refs stale evidence
		},
	}
	staleEv, staleClaims := ReconcileState("B", st)

	if len(staleEv) != 1 || staleEv[0].ID != "ev1" {
		t.Fatalf("stale evidence = %+v, want [ev1]", idsOfEv(staleEv))
	}
	wantClaims := map[string]bool{"c1": true, "c3": true, "c6": true}
	if len(staleClaims) != len(wantClaims) {
		t.Fatalf("stale claims = %+v, want %v", idsOfCl(staleClaims), wantClaims)
	}
	for _, c := range staleClaims {
		if !wantClaims[c.ID] {
			t.Fatalf("unexpected stale claim %s", c.ID)
		}
	}
}

func TestReconcileStateNoState(t *testing.T) {
	st := &State{
		Evidence: []*Evidence{{ID: "ev1", CodeState: "A", Status: "VALID"}},
		Claims:   []*Claim{{ID: "c1", Status: ClaimVerified, CodeState: "A"}},
	}
	// Unknown current state → nothing to judge.
	if ev, cl := ReconcileState("", st); len(ev) != 0 || len(cl) != 0 {
		t.Fatalf("empty current state should be a no-op")
	}
}

func TestStoreUpdateEvidence(t *testing.T) {
	root := t.TempDir()
	s, err := NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ev := &Evidence{ID: NewID("ev"), Kind: EvidenceTestResult, Status: "VALID", CodeState: "A"}
	if err := s.AddEvidence(ev); err != nil {
		t.Fatal(err)
	}
	ev.Status = EvidenceStale
	if err := s.UpdateEvidence(ev); err != nil {
		t.Fatal(err)
	}
	if got := s.State.Evidence[0]; got.Status != EvidenceStale {
		t.Fatalf("evidence status = %s, want %s", got.Status, EvidenceStale)
	}
	// Reopen: the update must survive replay from the event log.
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if got := s2.State.Evidence[0]; got.Status != EvidenceStale {
		t.Fatalf("replayed evidence status = %s, want %s", got.Status, EvidenceStale)
	}
}

func idsOfEv(evs []*Evidence) []string {
	out := make([]string, 0, len(evs))
	for _, e := range evs {
		out = append(out, e.ID)
	}
	return out
}

func idsOfCl(cls []*Claim) []string {
	out := make([]string, 0, len(cls))
	for _, c := range cls {
		out = append(out, c.ID)
	}
	return out
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

func TestCompilerRanksClaimsByGoal(t *testing.T) {
	now := time.Now()
	st := &State{
		Goals: []*Goal{{
			ID: "g1", Description: "fix jwt auth for the api", Status: StatusActive,
			CreatedAt: now, UpdatedAt: now,
		}},
		Claims: []*Claim{
			{ID: "c1", Subject: "database layer", Predicate: "uses", Object: "gorm",
				Status: ClaimVerified, Confidence: 0.9, CreatedAt: now, UpdatedAt: now},
			{ID: "c2", Subject: "jwt auth middleware", Predicate: "works", Object: "in api",
				Status: ClaimVerified, Confidence: 0.85, CreatedAt: now, UpdatedAt: now},
			{ID: "c3", Subject: "payment api", Predicate: "fails", Object: "on retries",
				Status: ClaimContradicted, Confidence: 0.6, CreatedAt: now, UpdatedAt: now},
		},
	}
	out := NewCompiler().Compile(st, nil)
	iJwt := strings.Index(out, "jwt auth middleware")
	iDb := strings.Index(out, "database layer")
	if iJwt < 0 || iDb < 0 {
		t.Fatalf("claims missing from compile output:\n%s", out)
	}
	if iJwt > iDb {
		t.Fatalf("goal-relevant claim should rank before unrelated claim:\n%s", out)
	}
	// The counts header must still report all claims.
	if !strings.Contains(out, "CLAIMS: 3 total | 2 verified | 0 supported | 1 contradicted") {
		t.Fatalf("counts header wrong:\n%s", out)
	}
}

func TestCompilerCapsClaims(t *testing.T) {
	now := time.Now()
	st := &State{}
	for i := 0; i < 12; i++ {
		st.Claims = append(st.Claims, &Claim{
			ID: fmt.Sprintf("c%d", i), Subject: "claim", Predicate: "n", Object: fmt.Sprint(i),
			Status: ClaimVerified, Confidence: 0.9, CreatedAt: now, UpdatedAt: now,
		})
	}
	c := NewCompiler()
	c.MaxClaims = 3
	out := c.Compile(st, nil)
	if strings.Count(out, "- [VERIFIED]") != 3 {
		t.Fatalf("expected exactly 3 claim lines:\n%s", out)
	}
	if !strings.Contains(out, "9 more claim(s)") {
		t.Fatalf("expected truncation note:\n%s", out)
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
