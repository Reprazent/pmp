package backend

import (
	"errors"
	"fmt"
	"strings"
)

// Sentinel errors. Backends wrap these so callers can use errors.Is.
var (
	// ErrUnavailable means the backend can't be used right now: its CLI is
	// missing, it's locked, or it isn't signed in.
	ErrUnavailable = errors.New("backend unavailable")
	// ErrNotFound means the backend works but the secret doesn't exist.
	ErrNotFound = errors.New("secret not found")
	// ErrAmbiguous means a reference matched more than one secret.
	ErrAmbiguous = errors.New("ambiguous reference")
	// ErrTimeout means a backend call took longer than --timeout.
	ErrTimeout = errors.New("timed out")
	// ErrInvalidRef means a reference is syntactically wrong for its backend.
	ErrInvalidRef = errors.New("invalid reference")
)

// UnavailableError explains why a backend can't be used, with an optional
// hint for the user.
type UnavailableError struct {
	Scheme string
	Reason string
	Hint   string
}

func (e *UnavailableError) Error() string {
	msg := fmt.Sprintf("%s: %s", e.Scheme, e.Reason)
	if e.Hint != "" {
		msg += " (" + e.Hint + ")"
	}
	return msg
}

func (e *UnavailableError) Unwrap() error { return ErrUnavailable }

// AmbiguousError lists the candidates a reference matched.
type AmbiguousError struct {
	Candidates []string
}

func (e *AmbiguousError) Error() string {
	return fmt.Sprintf("%s: matches %s", ErrAmbiguous, strings.Join(e.Candidates, ", "))
}

func (e *AmbiguousError) Unwrap() error { return ErrAmbiguous }

// RefError ties an error to the reference it happened for. It never
// contains a secret value.
type RefError struct {
	Ref string
	Err error
}

func (e *RefError) Error() string { return e.Ref + ": " + e.Err.Error() }
func (e *RefError) Unwrap() error { return e.Err }

// MultiError collects errors from several references or backends.
type MultiError []error

func (m MultiError) Error() string {
	parts := make([]string, len(m))
	for i, err := range m {
		parts[i] = err.Error()
	}
	return strings.Join(parts, "\n")
}

func (m MultiError) Unwrap() []error { return m }

// ErrOrNil returns nil for an empty MultiError, the single error if there's
// only one, and the MultiError otherwise.
func (m MultiError) ErrOrNil() error {
	switch len(m) {
	case 0:
		return nil
	case 1:
		return m[0]
	default:
		return m
	}
}
