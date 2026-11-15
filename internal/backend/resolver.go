package backend

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Reprazent/pmp/internal/ref"
)

// Resolver resolves references across backends.
type Resolver struct {
	Registry *Registry
	// Timeout applies to each backend's Resolve call. Zero means none.
	Timeout time.Duration
	// SkipUnavailable drops references whose backend is unavailable instead
	// of failing. Missing secrets in a working backend still fail.
	SkipUnavailable bool
	// Warnf reports skipped references. May be nil.
	Warnf func(format string, args ...any)
	// Debugf logs progress. Never called with secret values. May be nil.
	Debugf func(format string, args ...any)
}

// Resolve returns values keyed by Ref.Raw. With SkipUnavailable, skipped
// references are absent from the map. Any other failure returns an error and
// no values, so callers can't accidentally use a partial result.
func (r *Resolver) Resolve(ctx context.Context, refs []ref.Ref) (map[string]string, error) {
	groups := map[string][]ref.Ref{}
	seen := map[string]bool{}
	var errs MultiError
	for _, rf := range refs {
		if seen[rf.Raw] {
			continue
		}
		seen[rf.Raw] = true
		if _, ok := r.Registry.Lookup(rf.Scheme); !ok {
			errs = append(errs, &RefError{Ref: rf.Raw, Err: fmt.Errorf("%w: unknown scheme %q", ErrInvalidRef, rf.Scheme)})
			continue
		}
		groups[rf.Scheme] = append(groups[rf.Scheme], rf)
	}
	if len(errs) > 0 {
		return nil, errs.ErrOrNil()
	}

	type result struct {
		values map[string]string
		errs   []error
	}
	results := make(map[string]*result, len(groups))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for scheme, group := range groups {
		b, _ := r.Registry.Lookup(scheme)
		wg.Add(1)
		go func() {
			defer wg.Done()
			vals, errs := r.resolveBackend(ctx, b, group)
			mu.Lock()
			results[scheme] = &result{values: vals, errs: errs}
			mu.Unlock()
		}()
	}
	wg.Wait()

	out := map[string]string{}
	for _, res := range results {
		errs = append(errs, res.errs...)
		for k, v := range res.values {
			out[k] = v
		}
	}
	if len(errs) > 0 {
		return nil, errs.ErrOrNil()
	}
	return out, nil
}

func (r *Resolver) resolveBackend(ctx context.Context, b Backend, refs []ref.Ref) (map[string]string, []error) {
	start := time.Now()
	r.debugf("%s: resolving %d reference(s)", b.Scheme(), len(refs))

	var vals map[string]string
	err := r.prepare(ctx, b, refs)
	if err == nil {
		vals, err = r.timedResolve(ctx, b, refs)
	}
	r.debugf("%s: done in %s", b.Scheme(), time.Since(start).Round(time.Millisecond))

	var errs []error
	if err != nil {
		var multi MultiError
		if errors.As(err, &multi) {
			errs = multi
		} else {
			errs = []error{err}
		}
	}

	// A backend-wide error (not tied to one ref) applies to every ref that
	// didn't get a value.
	var refErrs []error
	backendWide := false
	for _, e := range errs {
		var re *RefError
		if errors.As(e, &re) {
			refErrs = append(refErrs, e)
		} else {
			backendWide = true
			refErrs = append(refErrs, e)
		}
	}

	if r.SkipUnavailable {
		kept := refErrs[:0]
		for _, e := range refErrs {
			if errors.Is(e, ErrUnavailable) {
				var re *RefError
				if errors.As(e, &re) {
					r.warnf("skipping %s", e)
				} else {
					r.warnf("skipping %d %s:// reference(s): %s", len(refs)-len(vals), b.Scheme(), e)
				}
				continue
			}
			kept = append(kept, e)
		}
		refErrs = kept
		if len(refErrs) == 0 && backendWide {
			// Everything without a value was skipped.
			return vals, nil
		}
	}

	if len(refErrs) == 0 && !backendWide {
		// Defensive: a backend that forgot to report a missing ref.
		for _, rf := range refs {
			if _, ok := vals[rf.Raw]; !ok {
				refErrs = append(refErrs, &RefError{Ref: rf.Raw, Err: ErrNotFound})
			}
		}
	}
	return vals, refErrs
}

func (r *Resolver) prepare(ctx context.Context, b Backend, refs []ref.Ref) error {
	p, ok := b.(Preparer)
	if !ok {
		return nil
	}
	return p.Prepare(ctx, refs)
}

func (r *Resolver) timedResolve(ctx context.Context, b Backend, refs []ref.Ref) (map[string]string, error) {
	if r.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.Timeout)
		defer cancel()
	}
	vals, err := b.Resolve(ctx, refs)
	if err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) && !errors.Is(err, ErrTimeout) {
		err = fmt.Errorf("%s: %w after %s", b.Scheme(), ErrTimeout, r.Timeout)
	}
	return vals, err
}

func (r *Resolver) warnf(format string, args ...any) {
	if r.Warnf != nil {
		r.Warnf(format, args...)
	}
}

func (r *Resolver) debugf(format string, args ...any) {
	if r.Debugf != nil {
		r.Debugf(format, args...)
	}
}
