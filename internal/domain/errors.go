package domain

import (
	"errors"
	"fmt"
)

// Sentinel errors. Handlers map these onto stable API error codes.
var (
	ErrNotFound         = errors.New("resource not found")
	ErrPermissionDenied = errors.New("permission denied")
	ErrValidation       = errors.New("validation failed")
)

// ValidationError describes client-correctable input problems. Fields maps an
// input field to the reason it was rejected.
type ValidationError struct {
	Fields map[string]string
}

// Unwrap lets callers match ValidationError with errors.Is(err, ErrValidation).
func (e *ValidationError) Unwrap() error { return ErrValidation }

func (e *ValidationError) Error() string {
	if len(e.Fields) == 0 {
		return "validation failed"
	}
	first := ""
	for field, reason := range e.Fields {
		first = field + ": " + reason
		break
	}
	return "validation failed (" + first + ")"
}

// ConflictError reports an optimistic concurrency or uniqueness conflict.
type ConflictError struct {
	// Code is "version_mismatch" or "duplicate_name".
	Code    string
	Message string
	// CurrentVersion is set for version_mismatch so clients can reload.
	CurrentVersion int64
}

func (e *ConflictError) Error() string { return e.Code + ": " + e.Message }

// Conflict codes.
const (
	ConflictVersionMismatch = "version_mismatch"
	ConflictDuplicateName   = "duplicate_name"
)

// NewVersionConflict builds the canonical optimistic concurrency error.
func NewVersionConflict(current int64) *ConflictError {
	return &ConflictError{
		Code:           ConflictVersionMismatch,
		Message:        "record was modified by someone else; reload and retry",
		CurrentVersion: current,
	}
}

// NewDuplicateName builds a uniqueness conflict for an entity type.
func NewDuplicateName(entity, name string) *ConflictError {
	return &ConflictError{
		Code:    ConflictDuplicateName,
		Message: fmt.Sprintf("a %s named %q already exists in this organization", entity, name),
	}
}

// IsConflict reports whether err is a *ConflictError.
func IsConflict(err error) bool {
	var conflict *ConflictError
	return errors.As(err, &conflict)
}

// IsValidation reports whether err is a *ValidationError.
func IsValidation(err error) bool {
	var validation *ValidationError
	return errors.As(err, &validation)
}

// IsNotFound reports whether err is the not-found sentinel.
func IsNotFound(err error) bool { return errors.Is(err, ErrNotFound) }

// IsPermissionDenied reports whether err is the permission-denied sentinel.
func IsPermissionDenied(err error) bool { return errors.Is(err, ErrPermissionDenied) }
