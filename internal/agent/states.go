// Package agent defines the durable run state machine. Transitions are legal
// only through the table below, so a run can never skip verification or jump
// straight to a merged state.
package agent

import (
	"errors"
	"fmt"
)

// Run states, mirrored by the database CHECK constraint on runs.state.
const (
	StateQueued           = "queued"
	StateIntaking         = "intaking"
	StateAnalyzing        = "analyzing"
	StatePlanning         = "planning"
	StateAwaitingApproval = "awaiting_approval"
	StateEditing          = "editing"
	StateTesting          = "testing"
	StateReviewing        = "reviewing"
	StatePRReady          = "pr_ready"
	StatePROpen           = "pr_open"
	StateCompleted        = "completed"
	StateFailed           = "failed"
	StateCanceled         = "canceled"
	StateBlocked          = "blocked"
)

// Terminal states cannot be left.
var terminalStates = map[string]bool{
	StateCompleted: true,
	StateCanceled:  true,
}

// transitions lists every legal from → to edge.
var transitions = map[string]map[string]bool{
	StateQueued: {
		StateIntaking: true, StateCanceled: true, StateFailed: true, StateBlocked: true,
	},
	StateIntaking: {
		StateAnalyzing: true, StateFailed: true, StateCanceled: true, StateBlocked: true,
	},
	StateAnalyzing: {
		StatePlanning: true, StateEditing: true,
		StateFailed: true, StateCanceled: true, StateBlocked: true,
	},
	StatePlanning: {
		StateAwaitingApproval: true, StateEditing: true,
		StateFailed: true, StateCanceled: true, StateBlocked: true,
	},
	StateAwaitingApproval: {
		StateEditing: true, StateCanceled: true, StateFailed: true, StateBlocked: true,
	},
	StateEditing: {
		StateTesting: true, StateAnalyzing: true,
		StateFailed: true, StateCanceled: true, StateBlocked: true,
	},
	StateTesting: {
		StateEditing: true, StateReviewing: true, StateAnalyzing: true,
		StateFailed: true, StateCanceled: true, StateBlocked: true,
	},
	StateReviewing: {
		StatePRReady: true, StateEditing: true, StateTesting: true,
		StateFailed: true, StateCanceled: true, StateBlocked: true,
	},
	StatePRReady: {
		StatePROpen: true, StateReviewing: true,
		StateFailed: true, StateCanceled: true, StateBlocked: true,
	},
	StatePROpen: {
		StateCompleted: true, StateFailed: true, StateCanceled: true, StateBlocked: true,
	},
	StateFailed: {
		StateQueued: true, StateAnalyzing: true, StateEditing: true,
	},
	StateBlocked: {
		StateQueued: true, StateAnalyzing: true, StateEditing: true,
		StateFailed: true, StateCanceled: true,
	},
}

// Known reports whether state is a valid run state.
func Known(state string) bool {
	_, ok := transitions[state]
	if ok {
		return true
	}
	return terminalStates[state]
}

// CanTransition reports whether moving from → to is legal.
func CanTransition(from, to string) bool {
	allowed, ok := transitions[from]
	if !ok {
		return false
	}
	return allowed[to]
}

// Transition validates a move and returns a typed conflict error when it is
// illegal.
func Transition(from, to string) error {
	if from == to {
		return &ConflictError{Code: ConflictSameState, Message: fmt.Sprintf("run is already %s", from)}
	}
	if terminalStates[from] {
		return &ConflictError{
			Code:    ConflictTerminal,
			Message: fmt.Sprintf("run is in the terminal state %s and cannot move to %s", from, to),
		}
	}
	if !CanTransition(from, to) {
		return &ConflictError{
			Code:    ConflictIllegal,
			Message: fmt.Sprintf("cannot move a run from %s to %s", from, to),
		}
	}
	return nil
}

// Conflict codes for illegal transitions.
const (
	ConflictSameState = "same_state"
	ConflictTerminal  = "terminal_state"
	ConflictIllegal   = "illegal_transition"
)

// ConflictError describes a rejected transition.
type ConflictError struct {
	Code    string
	Message string
}

func (e *ConflictError) Error() string { return e.Message }

// IsConflict reports whether err is an agent conflict.
func IsConflict(err error) bool {
	var conflict *ConflictError
	return errors.As(err, &conflict)
}

// States returns every known state, for documentation and tests.
func States() []string {
	return []string{
		StateQueued, StateIntaking, StateAnalyzing, StatePlanning, StateAwaitingApproval,
		StateEditing, StateTesting, StateReviewing, StatePRReady, StatePROpen,
		StateCompleted, StateFailed, StateCanceled, StateBlocked,
	}
}

// ErrRunNotFound is returned when a run does not exist in the caller's scope.
var ErrRunNotFound = errors.New("agent: run not found")
