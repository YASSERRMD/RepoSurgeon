package agent

import "testing"

func TestKnownStates(t *testing.T) {
	for _, state := range States() {
		if !Known(state) {
			t.Fatalf("state %q reported unknown", state)
		}
	}
	if Known("nonsense") {
		t.Fatal("unknown state accepted")
	}
}

func TestLegalTransitions(t *testing.T) {
	legal := [][2]string{
		{StateQueued, StateIntaking},
		{StateIntaking, StateAnalyzing},
		{StateAnalyzing, StatePlanning},
		{StatePlanning, StateAwaitingApproval},
		{StateAwaitingApproval, StateEditing},
		{StateEditing, StateTesting},
		{StateTesting, StateReviewing},
		{StateReviewing, StatePRReady},
		{StatePRReady, StatePROpen},
		{StatePROpen, StateCompleted},
		{StateTesting, StateEditing},   // repair loop
		{StateEditing, StateAnalyzing}, // re-observe after failure
		{StateFailed, StateQueued},     // retry
		{StateBlocked, StateAnalyzing}, // unblock
	}
	for _, pair := range legal {
		if err := Transition(pair[0], pair[1]); err != nil {
			t.Fatalf("%s → %s rejected: %v", pair[0], pair[1], err)
		}
	}
}

func TestIllegalTransitions(t *testing.T) {
	illegal := [][2]string{
		{StateQueued, StateCompleted}, // cannot skip work
		{StateQueued, StatePROpen},
		{StateIntaking, StateReviewing},
		{StateAnalyzing, StateCompleted},
		{StatePlanning, StateCompleted},
		{StateEditing, StatePRReady},
		{StatePROpen, StateEditing},
		{StateCompleted, StateQueued}, // terminal
		{StateCanceled, StateEditing},
		{"", StateQueued},
		{StateQueued, ""},
	}
	for _, pair := range illegal {
		err := Transition(pair[0], pair[1])
		if err == nil {
			t.Fatalf("%s → %s was accepted", pair[0], pair[1])
		}
		var conflict *ConflictError
		if !asConflict(err, &conflict) {
			t.Fatalf("%s → %s returned %v, want *ConflictError", pair[0], pair[1], err)
		}
	}
}

func TestSameStateConflict(t *testing.T) {
	err := Transition(StateEditing, StateEditing)
	if err == nil {
		t.Fatal("same-state transition must be rejected")
	}
	var conflict *ConflictError
	if !asConflict(err, &conflict) || conflict.Code != ConflictSameState {
		t.Fatalf("err = %v", err)
	}
}

func asConflict(err error, target **ConflictError) bool {
	if conflict, ok := err.(*ConflictError); ok {
		*target = conflict
		return true
	}
	return false
}
