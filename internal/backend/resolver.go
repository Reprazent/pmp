package backend

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Reprazent/pmp/internal/cache"
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
	// Cache, when set together with a positive CacheTTL, serves cacheable
	// references without asking their backend and stores fresh values.
	Cache    cache.Store
	CacheTTL time.Duration
	// Refresh skips cache reads but still writes.
	Refresh bool
	// Now defaults to time.Now.
	Now func() time.Time
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

	out := r.cacheLookup(ctx, groups)

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

	fresh := map[string]string{}
	for _, res := range results {
		errs = append(errs, res.errs...)
		for k, v := range res.values {
			out[k] = v
			fresh[k] = v
		}
	}
	// Store what did resolve even if something else failed, so a retry
	// only has to fetch the failures.
	r.cacheStore(ctx, groups, fresh)
	if len(errs) > 0 {
		return nil, errs.ErrOrNil()
	}
	return out, nil
}

func (r *Resolver) cacheEnabled() bool { return r.Cache != nil && r.CacheTTL > 0 }

func (r *Resolver) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// cacheCtx bounds one cache phase (lookup or store) by Timeout, like a
// backend call, so a stuck Keychain can't hang pmp.
func (r *Resolver) cacheCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	if r.Timeout > 0 {
		return context.WithTimeout(ctx, r.Timeout)
	}
	return context.WithCancel(ctx)
}

// isOTPSeed reports whether v is an otpauth:// URI. Those hold the seed that
// generates one-time passwords, like a 1Password OTP field read without
// ?attribute=otp, so they're never cached, whatever the backend says.
func isOTPSeed(v string) bool {
	const scheme = "otpauth://"
	v = strings.TrimSpace(v)
	return len(v) >= len(scheme) && strings.EqualFold(v[:len(scheme)], scheme)
}

func (r *Resolver) cacheable(rf ref.Ref) bool {
	b, ok := r.Registry.Lookup(rf.Scheme)
	if !ok {
		return false
	}
	c, ok := b.(Cacheable)
	return ok && c.Cacheable(rf)
}

// cacheLookup returns cached values and removes them from groups (and drops
// groups that end up empty), so their backends aren't asked at all.
func (r *Resolver) cacheLookup(ctx context.Context, groups map[string][]ref.Ref) map[string]string {
	out := map[string]string{}
	if !r.cacheEnabled() || r.Refresh {
		return out
	}
	ctx, cancel := r.cacheCtx(ctx)
	defer cancel()
	now := r.now()
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, group := range groups {
		for _, rf := range group {
			if !r.cacheable(rf) {
				continue
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				e, ok, err := r.Cache.Get(ctx, rf.Raw)
				switch {
				case err != nil:
					r.warnf("cache: reading %s: %v", rf.Raw, err)
					return
				case !ok:
					r.debugf("cache: miss %s", rf.Raw)
					return
				case e.Expired(now):
					r.debugf("cache: expired %s", rf.Raw)
					return
				}
				r.debugf("cache: hit %s", rf.Raw)
				mu.Lock()
				out[rf.Raw] = e.Value
				mu.Unlock()
			}()
		}
	}
	wg.Wait()

	for scheme, group := range groups {
		kept := group[:0]
		for _, rf := range group {
			if _, hit := out[rf.Raw]; !hit {
				kept = append(kept, rf)
			}
		}
		if len(kept) == 0 {
			delete(groups, scheme)
		} else {
			groups[scheme] = kept
		}
	}
	return out
}

// cacheStore writes freshly resolved, cacheable values. It waits for the
// writes so they finish before pmp exits or execs.
func (r *Resolver) cacheStore(ctx context.Context, groups map[string][]ref.Ref, fresh map[string]string) {
	if !r.cacheEnabled() {
		return
	}
	ctx, cancel := r.cacheCtx(ctx)
	defer cancel()
	expires := r.now().Add(r.CacheTTL)
	var wg sync.WaitGroup
	for _, group := range groups {
		for _, rf := range group {
			v, ok := fresh[rf.Raw]
			if !ok || !r.cacheable(rf) {
				continue
			}
			if isOTPSeed(v) {
				r.debugf("cache: not caching %s, it's a one-time password seed", rf.Raw)
				continue
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				err := r.Cache.Set(ctx, cache.Entry{Ref: rf.Raw, Value: v, Expires: expires})
				switch {
				case errors.Is(err, cache.ErrTooBig):
					r.debugf("cache: %s is too big to cache", rf.Raw)
				case err != nil:
					r.warnf("cache: writing %s: %v", rf.Raw, err)
				default:
					r.debugf("cache: stored %s until %s", rf.Raw, expires.Format(time.RFC3339))
				}
			}()
		}
	}
	wg.Wait()
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
