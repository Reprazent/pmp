package backend_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Reprazent/pmp/internal/backend"
	"github.com/Reprazent/pmp/internal/backend/fake"
	"github.com/Reprazent/pmp/internal/cache"
)

// ctx is the context for cache calls made directly by tests.
var ctx = context.Background()

var epoch = time.Unix(1_800_000_000, 0)

func cachingResolver(store cache.Store, bs ...backend.Backend) *backend.Resolver {
	return &backend.Resolver{
		Registry: backend.NewRegistry(bs...),
		Cache:    store,
		CacheTTL: time.Hour,
		Now:      func() time.Time { return epoch },
	}
}

func TestResolverCacheMissThenHit(t *testing.T) {
	op := &fake.Backend{Name: "op", Secrets: map[string]string{"op://a/b/c": "1", "op://a/b/d": "2"}}
	store := cache.NewMemory()
	r := cachingResolver(store, op)

	for i := range 2 {
		got, err := r.Resolve(context.Background(), refs(t, "op://a/b/c", "op://a/b/d"))
		if err != nil {
			t.Fatal(err)
		}
		if want := map[string]string{"op://a/b/c": "1", "op://a/b/d": "2"}; !reflect.DeepEqual(got, want) {
			t.Errorf("run %d: got %v, want %v", i, got, want)
		}
	}
	if len(op.Calls) != 1 {
		t.Errorf("backend calls = %v, want only the first run", op.Calls)
	}
	e, ok, _ := store.Get(ctx, "op://a/b/c")
	if !ok || e.Value != "1" || !e.Expires.Equal(epoch.Add(time.Hour)) {
		t.Errorf("stored entry = %+v, %v", e, ok)
	}
}

func TestResolverCachePartialHit(t *testing.T) {
	op := &fake.Backend{Name: "op", Secrets: map[string]string{"op://a/b/c": "1", "op://a/b/d": "2"}}
	store := cache.NewMemory()
	_ = store.Set(ctx, cache.Entry{Ref: "op://a/b/c", Value: "cached", Expires: epoch.Add(time.Minute)})
	r := cachingResolver(store, op)

	got, err := r.Resolve(context.Background(), refs(t, "op://a/b/c", "op://a/b/d"))
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]string{"op://a/b/c": "cached", "op://a/b/d": "2"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if want := [][]string{{"op://a/b/d"}}; !reflect.DeepEqual(op.Calls, want) {
		t.Errorf("backend calls = %v, want %v", op.Calls, want)
	}
}

func TestResolverCacheExpired(t *testing.T) {
	op := &fake.Backend{Name: "op", Secrets: map[string]string{"op://a/b/c": "fresh"}}
	store := cache.NewMemory()
	_ = store.Set(ctx, cache.Entry{Ref: "op://a/b/c", Value: "stale", Expires: epoch})
	r := cachingResolver(store, op)

	got, err := r.Resolve(context.Background(), refs(t, "op://a/b/c"))
	if err != nil {
		t.Fatal(err)
	}
	if got["op://a/b/c"] != "fresh" {
		t.Errorf("got %q, want fresh value", got["op://a/b/c"])
	}
	if e, _, _ := store.Get(ctx, "op://a/b/c"); e.Value != "fresh" {
		t.Errorf("cache not refreshed: %+v", e)
	}
}

func TestResolverCacheRefresh(t *testing.T) {
	op := &fake.Backend{Name: "op", Secrets: map[string]string{"op://a/b/c": "fresh"}}
	store := cache.NewMemory()
	_ = store.Set(ctx, cache.Entry{Ref: "op://a/b/c", Value: "old", Expires: epoch.Add(time.Hour)})
	r := cachingResolver(store, op)
	r.Refresh = true

	got, err := r.Resolve(context.Background(), refs(t, "op://a/b/c"))
	if err != nil {
		t.Fatal(err)
	}
	if got["op://a/b/c"] != "fresh" || len(op.Calls) != 1 {
		t.Errorf("got %v with calls %v, want a fresh lookup", got, op.Calls)
	}
	if e, _, _ := store.Get(ctx, "op://a/b/c"); e.Value != "fresh" {
		t.Errorf("--refresh didn't write: %+v", e)
	}
}

func TestResolverCacheSkipsUncacheable(t *testing.T) {
	bw := &fake.Backend{Name: "bw", Secrets: map[string]string{"bw://x/totp": "123456"}, NoCache: map[string]bool{"bw://x/totp": true}}
	store := cache.NewMemory()
	_ = store.Set(ctx, cache.Entry{Ref: "bw://x/totp", Value: "000000", Expires: epoch.Add(time.Hour)})
	r := cachingResolver(store, bw)

	got, err := r.Resolve(context.Background(), refs(t, "bw://x/totp"))
	if err != nil {
		t.Fatal(err)
	}
	if got["bw://x/totp"] != "123456" {
		t.Errorf("got %q, want the backend's value", got["bw://x/totp"])
	}
	if e, _, _ := store.Get(ctx, "bw://x/totp"); e.Value != "000000" {
		t.Errorf("uncacheable value was written: %+v", e)
	}
}

// A backend without the Cacheable method is never cached.
type plain struct{ fake.Backend }

func (*plain) Cacheable() {} // shadows fake.Backend.Cacheable with the wrong signature

func TestResolverCacheRequiresOptIn(t *testing.T) {
	b := &plain{fake.Backend{Name: "op", Secrets: map[string]string{"op://a/b/c": "1"}}}
	store := cache.NewMemory()
	r := cachingResolver(store, b)
	if _, err := r.Resolve(context.Background(), refs(t, "op://a/b/c")); err != nil {
		t.Fatal(err)
	}
	if es, _ := store.List(ctx); len(es) != 0 {
		t.Errorf("cached %v from a backend that didn't opt in", es)
	}
}

func TestResolverCacheHitSkipsUnavailableBackend(t *testing.T) {
	bw := &fake.Backend{Name: "bw", Err: &backend.UnavailableError{Scheme: "bw", Reason: "locked"}, PrepareErr: errors.New("would prompt")}
	store := cache.NewMemory()
	_ = store.Set(ctx, cache.Entry{Ref: "bw://x/y", Value: "v", Expires: epoch.Add(time.Hour)})
	r := cachingResolver(store, bw)

	got, err := r.Resolve(context.Background(), refs(t, "bw://x/y"))
	if err != nil {
		t.Fatal(err)
	}
	if got["bw://x/y"] != "v" || len(bw.Calls) != 0 {
		t.Errorf("got %v, calls %v; want a cache hit without touching the backend", got, bw.Calls)
	}
}

func TestResolverCacheErrorsFallBack(t *testing.T) {
	op := &fake.Backend{Name: "op", Secrets: map[string]string{"op://a/b/c": "1"}}
	store := cache.NewMemory()
	store.Err = errors.New("keychain on fire")
	r := cachingResolver(store, op)
	var warnings []string
	r.Warnf = func(format string, args ...any) { warnings = append(warnings, format) }

	got, err := r.Resolve(context.Background(), refs(t, "op://a/b/c"))
	if err != nil {
		t.Fatal(err)
	}
	if got["op://a/b/c"] != "1" {
		t.Errorf("got %v", got)
	}
	if len(warnings) != 2 || !strings.Contains(warnings[0], "cache") {
		t.Errorf("warnings = %v, want one for the read and one for the write", warnings)
	}
}

func TestResolverCachesSuccessesOnFailure(t *testing.T) {
	op := &fake.Backend{Name: "op", Secrets: map[string]string{"op://a/b/c": "1"}}
	store := cache.NewMemory()
	r := cachingResolver(store, op)
	if _, err := r.Resolve(context.Background(), refs(t, "op://a/b/c", "op://a/b/missing")); err == nil {
		t.Fatal("want an error")
	}
	es, _ := store.List(ctx)
	if len(es) != 1 || es[0].Ref != "op://a/b/c" {
		t.Errorf("cached %v, want only the value that resolved", es)
	}
}

func TestResolverCacheOffWithoutTTL(t *testing.T) {
	op := &fake.Backend{Name: "op", Secrets: map[string]string{"op://a/b/c": "1"}}
	store := cache.NewMemory()
	r := cachingResolver(store, op)
	r.CacheTTL = 0
	if _, err := r.Resolve(context.Background(), refs(t, "op://a/b/c")); err != nil {
		t.Fatal(err)
	}
	if es, _ := store.List(ctx); len(es) != 0 {
		t.Errorf("cached %v with a zero TTL", es)
	}
}

// A cache that hangs (a Keychain behind an unlock dialog) is cut off by
// Timeout on both the read and the write, and pmp still gets the value from
// the backend.
func TestResolverCacheTimeout(t *testing.T) {
	op := &fake.Backend{Name: "op", Secrets: map[string]string{"op://a/b/c": "1"}}
	store := cache.NewMemory()
	store.Block = true
	r := cachingResolver(store, op)
	r.Timeout = 50 * time.Millisecond
	var warnings []string
	r.Warnf = func(format string, args ...any) { warnings = append(warnings, fmt.Sprintf(format, args...)) }

	start := time.Now()
	got, err := r.Resolve(context.Background(), refs(t, "op://a/b/c"))
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("took %s, want the cache cut off by the timeout", d)
	}
	if got["op://a/b/c"] != "1" || len(op.Calls) != 1 {
		t.Errorf("got %v with calls %v, want the backend's value", got, op.Calls)
	}
	if len(warnings) != 2 || !strings.Contains(warnings[0], "deadline exceeded") || !strings.Contains(warnings[1], "deadline exceeded") {
		t.Errorf("warnings = %q, want a timeout for the read and the write", warnings)
	}
}

// The caller's context also stops the cache, with or without Timeout.
func TestResolverCacheHonoursCallerContext(t *testing.T) {
	op := &fake.Backend{Name: "op", Secrets: map[string]string{"op://a/b/c": "1"}}
	store := cache.NewMemory()
	store.Block = true
	r := cachingResolver(store, op)
	cctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = r.Resolve(cctx, refs(t, "op://a/b/c"))
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Resolve hung on a blocked cache")
	}
}

// otpauth:// URIs hold a one-time password seed and are never cached, even
// from a reference the backend calls cacheable.
func TestResolverCacheSkipsOTPSeeds(t *testing.T) {
	op := &fake.Backend{Name: "op", Secrets: map[string]string{
		"op://a/github/one-time password": "otpauth://totp/GitHub:me?secret=JBSWY3DPEHPK3PXP&issuer=GitHub",
		"op://a/aws/notes":                "  OTPAUTH://totp/AWS?secret=JBSWY3DPEHPK3PXP\n",
		"op://a/github/token":             "ghp_not-an-otpauth://uri",
	}}
	store := cache.NewMemory()
	r := cachingResolver(store, op)

	got, err := r.Resolve(context.Background(), refs(t, "op://a/github/one-time password", "op://a/aws/notes", "op://a/github/token"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got["op://a/github/one-time password"], "otpauth://") {
		t.Errorf("seed not returned: %v", got)
	}
	es, _ := store.List(ctx)
	if len(es) != 1 || es[0].Ref != "op://a/github/token" {
		t.Errorf("cached %v, want only the token", es)
	}
}
