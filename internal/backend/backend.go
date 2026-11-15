// Package backend defines the interface password manager backends implement,
// a registry keyed by scheme, and a Resolver that fans references out to
// backends in parallel.
package backend

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/Reprazent/pmp/internal/ref"
)

// Status describes whether a backend can be used right now.
type Status struct {
	Available bool
	// Detail is a short human-readable explanation, e.g. "cli, signed in"
	// or "locked". It never contains secrets.
	Detail string
}

// Backend resolves references for one scheme.
type Backend interface {
	// Scheme is the reference scheme this backend handles, e.g. "op".
	Scheme() string
	// Status reports whether the backend can be used. It must not prompt.
	Status(ctx context.Context) Status
	// Resolve returns the value for each reference, keyed by Ref.Raw.
	// References that failed are left out of the map and reported in the
	// error, ideally as RefErrors (combined in a MultiError). A backend-wide
	// problem can be returned as a single error.
	Resolve(ctx context.Context, refs []ref.Ref) (map[string]string, error)
}

// Preparer is implemented by backends that need to do something before the
// timed Resolve call, such as prompting the user to unlock. Prepare runs
// without --timeout, since it may wait for a human.
type Preparer interface {
	Prepare(ctx context.Context, refs []ref.Ref) error
}

// Registry holds backends by scheme.
type Registry struct {
	mu       sync.RWMutex
	backends map[string]Backend
}

// NewRegistry returns a registry containing the given backends.
func NewRegistry(backends ...Backend) *Registry {
	r := &Registry{backends: map[string]Backend{}}
	for _, b := range backends {
		r.Register(b)
	}
	return r
}

// Register adds b. Registering the same scheme twice panics, since that's a
// programming error.
func (r *Registry) Register(b Backend) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.backends[b.Scheme()]; dup {
		panic(fmt.Sprintf("backend: scheme %q registered twice", b.Scheme()))
	}
	r.backends[b.Scheme()] = b
}

// Lookup returns the backend for scheme.
func (r *Registry) Lookup(scheme string) (Backend, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	b, ok := r.backends[scheme]
	return b, ok
}

// Schemes returns the registered schemes, sorted.
func (r *Registry) Schemes() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.backends))
	for s := range r.backends {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
