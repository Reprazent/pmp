// Package exitcode maps errors onto pmp's exit codes (spec 5.4).
package exitcode

import (
	"context"
	"errors"

	"github.com/Reprazent/pmp/internal/backend"
)

// Exit codes.
const (
	OK          = 0
	Generic     = 1
	Usage       = 2
	Unavailable = 3
	NotFound    = 4
	Timeout     = 5
)

// UsageError marks an error as a usage problem (exit 2).
type UsageError struct{ Err error }

func (e *UsageError) Error() string { return e.Err.Error() }
func (e *UsageError) Unwrap() error { return e.Err }

// NewUsage wraps err as a usage error.
func NewUsage(err error) error { return &UsageError{Err: err} }

// ExitError carries an explicit exit code without a message, for example
// when `pmp backends` found an unavailable backend and already reported it.
type ExitError struct{ Code int }

func (e *ExitError) Error() string { return "exit status" }

// For returns the exit code for err. When err holds several errors
// (MultiError), the one with the highest priority wins:
// usage (2) > unavailable (3) > timeout (5) > not found (4) > generic (1).
func For(err error) int {
	if err == nil {
		return OK
	}
	var exit *ExitError
	if errors.As(err, &exit) {
		return exit.Code
	}
	var usage *UsageError
	switch {
	case errors.As(err, &usage), errors.Is(err, backend.ErrInvalidRef):
		return Usage
	case errors.Is(err, backend.ErrUnavailable):
		return Unavailable
	case errors.Is(err, backend.ErrTimeout), errors.Is(err, context.DeadlineExceeded):
		return Timeout
	case errors.Is(err, backend.ErrNotFound), errors.Is(err, backend.ErrAmbiguous):
		return NotFound
	default:
		return Generic
	}
}
