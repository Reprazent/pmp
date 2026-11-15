package exitcode

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Reprazent/pmp/internal/backend"
)

func TestFor(t *testing.T) {
	ref := func(err error) error { return &backend.RefError{Ref: "x://y", Err: err} }
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"nil", nil, OK},
		{"generic", errors.New("boom"), Generic},
		{"usage", NewUsage(errors.New("bad flag")), Usage},
		{"invalid ref", ref(backend.ErrInvalidRef), Usage},
		{"unavailable", ref(&backend.UnavailableError{Scheme: "bw", Reason: "locked"}), Unavailable},
		{"timeout", ref(backend.ErrTimeout), Timeout},
		{"deadline", fmt.Errorf("x: %w", context.DeadlineExceeded), Timeout},
		{"not found", ref(backend.ErrNotFound), NotFound},
		{"ambiguous", ref(&backend.AmbiguousError{Candidates: []string{"a", "b"}}), NotFound},
		{"explicit", &ExitError{Code: 3}, 3},
		{"priority usage beats all", backend.MultiError{
			ref(backend.ErrNotFound), ref(backend.ErrTimeout), ref(backend.ErrUnavailable), ref(backend.ErrInvalidRef),
		}, Usage},
		{"priority unavailable beats timeout", backend.MultiError{
			ref(backend.ErrNotFound), ref(backend.ErrTimeout), ref(backend.ErrUnavailable),
		}, Unavailable},
		{"priority timeout beats not found", backend.MultiError{
			ref(backend.ErrNotFound), ref(backend.ErrTimeout),
		}, Timeout},
		{"priority not found beats generic", backend.MultiError{
			errors.New("boom"), ref(backend.ErrNotFound),
		}, NotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := For(tt.err); got != tt.want {
				t.Errorf("For(%v) = %d, want %d", tt.err, got, tt.want)
			}
		})
	}
}
