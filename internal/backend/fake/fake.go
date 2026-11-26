// Package fake is an in-memory backend for tests.
package fake

import (
	"context"
	"sync"

	"github.com/Reprazent/pmp/internal/backend"
	"github.com/Reprazent/pmp/internal/ref"
)

// Backend serves Secrets for its scheme. Set Err to fail every call, or
// Errors to fail individual references.
type Backend struct {
	Name    string
	Secrets map[string]string
	// Errors fails specific references (keyed by Ref.Raw).
	Errors map[string]error
	// Err fails the whole call, e.g. with an UnavailableError.
	Err error
	// Block makes Resolve wait for the context, to test timeouts.
	Block bool
	// PrepareErr is returned from Prepare.
	PrepareErr error
	// NoCache lists references (by Ref.Raw) that aren't cacheable.
	NoCache map[string]bool

	mu    sync.Mutex
	Calls [][]string
}

func (b *Backend) Scheme() string { return b.Name }

func (b *Backend) Status(context.Context) backend.Status {
	if b.Err != nil {
		return backend.Status{Available: false, Detail: b.Err.Error()}
	}
	return backend.Status{Available: true, Detail: "fake"}
}

func (b *Backend) Cacheable(r ref.Ref) bool { return !b.NoCache[r.Raw] }

func (b *Backend) Prepare(context.Context, []ref.Ref) error { return b.PrepareErr }

func (b *Backend) Resolve(ctx context.Context, refs []ref.Ref) (map[string]string, error) {
	raws := make([]string, len(refs))
	for i, r := range refs {
		raws[i] = r.Raw
	}
	b.mu.Lock()
	b.Calls = append(b.Calls, raws)
	b.mu.Unlock()

	if b.Block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if b.Err != nil {
		return nil, b.Err
	}
	out := map[string]string{}
	var errs backend.MultiError
	for _, r := range refs {
		if err, ok := b.Errors[r.Raw]; ok {
			errs = append(errs, &backend.RefError{Ref: r.Raw, Err: err})
			continue
		}
		v, ok := b.Secrets[r.Raw]
		if !ok {
			errs = append(errs, &backend.RefError{Ref: r.Raw, Err: backend.ErrNotFound})
			continue
		}
		out[r.Raw] = v
	}
	return out, errs.ErrOrNil()
}
