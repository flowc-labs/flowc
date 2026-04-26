package store

import (
	"errors"
	"fmt"
)

var (
	ErrRevisionConflict  = errors.New("revision conflict: resource has been modified")
	ErrOwnershipConflict = errors.New("ownership conflict: resource is managed by another writer")
	ErrNotFound          = errors.New("resource not found")
	ErrAlreadyExists     = errors.New("resource already exists")
	ErrInvalidResource   = errors.New("invalid resource")
)

type RevisionConflictError struct {
	Key      ResourceKey
	Expected int64
	Actual   int64
}

func (e *RevisionConflictError) Error() string {
	return fmt.Sprintf("revision conflict on %s/%s: expected %d, actual %d", e.Key.Kind, e.Key.Name, e.Expected, e.Actual)
}

func (e *RevisionConflictError) Unwrap() error { return ErrRevisionConflict }

type OwnershipConflictError struct {
	Key          ResourceKey
	CurrentOwner string
	AttemptedBy  string
}

func (e *OwnershipConflictError) Error() string {
	return fmt.Sprintf("ownership conflict on %s/%s: owned by %q, attempted by %q", e.Key.Kind, e.Key.Name, e.CurrentOwner, e.AttemptedBy)
}

func (e *OwnershipConflictError) Unwrap() error { return ErrOwnershipConflict }
