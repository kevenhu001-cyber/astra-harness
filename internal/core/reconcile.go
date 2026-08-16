package core

// ReconcileState implements the evidence-invalidation half of the claim
// lifecycle: "code changed → old evidence must be re-evaluated". Given the
// current working-tree state hash, every evidence record bound to an older
// code state is flagged STALE, and every VERIFIED/SUPPORTED claim that was
// established against an older state (or that references stale evidence) is
// downgraded to STALE until re-verified.
//
// The function is pure (no I/O) so the transition rules are unit-testable;
// the engine applies the returned updates through the event-sourced store.
func ReconcileState(current string, st *State) (staleEvidence []*Evidence, staleClaims []*Claim) {
	if current == "" || st == nil {
		return nil, nil
	}

	for _, ev := range st.Evidence {
		if ev.CodeState != "" && ev.CodeState != current && ev.Status != EvidenceStale {
			staleEvidence = append(staleEvidence, ev)
		}
	}
	staleEvIDs := make(map[string]bool, len(staleEvidence))
	for _, ev := range staleEvidence {
		staleEvIDs[ev.ID] = true
	}

	for _, c := range st.Claims {
		if c.Status != ClaimVerified && c.Status != ClaimSupported {
			continue
		}
		if c.CodeState != "" && c.CodeState != current {
			staleClaims = append(staleClaims, c)
			continue
		}
		for _, id := range c.EvidenceIDs {
			if staleEvIDs[id] {
				staleClaims = append(staleClaims, c)
				break
			}
		}
	}
	return staleEvidence, staleClaims
}
