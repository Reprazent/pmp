package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Reprazent/pmp/internal/cache"
	"github.com/Reprazent/pmp/internal/exitcode"
)

// ctx is the context for cache calls made directly by tests.
var ctx = context.Background()

func cachedRefs(t *testing.T, store *cache.Memory) map[string]time.Duration {
	t.Helper()
	es, err := store.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]time.Duration{}
	for _, e := range es {
		out[e.Ref] = time.Until(e.Expires).Round(time.Hour)
	}
	return out
}

func TestGetCaches(t *testing.T) {
	op, bw := defaultBackends()
	store := cache.NewMemory()
	for range 2 {
		h := newHarness(t, op, bw)
		h.opts.Cache = store
		if code := h.run("get", "--cache-ttl=2h", "op://Private/GitHub/token"); code != 0 {
			t.Fatalf("exit %d: %s", code, h.stderr.String())
		}
		if h.stdout.String() != "gh-secret" {
			t.Errorf("stdout = %q", h.stdout.String())
		}
	}
	if len(op.Calls) != 1 {
		t.Errorf("op calls = %v, want the second get served from cache", op.Calls)
	}
	if got := cachedRefs(t, store)["op://Private/GitHub/token"]; got != 2*time.Hour {
		t.Errorf("ttl = %s, want 2h", got)
	}
}

func TestCacheTTLPrecedence(t *testing.T) {
	tests := []struct {
		name, flag, directive, env string
		want                       time.Duration // 0: nothing cached
	}{
		{name: "off by default"},
		{name: "env var", env: "3h", want: 3 * time.Hour},
		{name: "file beats env", directive: "4h", env: "3h", want: 4 * time.Hour},
		{name: "flag beats file", flag: "5h", directive: "4h", env: "3h", want: 5 * time.Hour},
		{name: "flag 0 turns it off", flag: "0", directive: "4h", env: "3h"},
		{name: "file 0 turns it off", directive: "0", env: "3h"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op, bw := defaultBackends()
			h := newHarness(t, op, bw)
			store := cache.NewMemory()
			h.opts.Cache = store
			if tt.env != "" {
				h.env["PMP_CACHE_TTL"] = tt.env
			}
			content := envFile
			if tt.directive != "" {
				content = "# pmp: cache-ttl=" + tt.directive + "\n" + content
			}
			args := []string{"env"}
			if tt.flag != "" {
				args = append(args, "--cache-ttl="+tt.flag)
			}
			args = append(args, writeFile(t, t.TempDir(), "env", content))
			if code := h.run(args...); code != 0 {
				t.Fatalf("exit %d: %s", code, h.stderr.String())
			}
			got := cachedRefs(t, store)
			if tt.want == 0 {
				if len(got) != 0 {
					t.Errorf("cached %v, want nothing", got)
				}
				return
			}
			if len(got) != 3 {
				t.Errorf("cached %v, want the 3 references", got)
			}
			for ref, ttl := range got {
				if ttl != tt.want {
					t.Errorf("%s ttl = %s, want %s", ref, ttl, tt.want)
				}
			}
		})
	}
}

func TestCacheTTLErrors(t *testing.T) {
	op, bw := defaultBackends()

	h := newHarness(t, op, bw)
	h.env["PMP_CACHE_TTL"] = "soon"
	if code := h.run("get", "op://Private/GitHub/token"); code != exitcode.Usage {
		t.Errorf("bad PMP_CACHE_TTL: exit %d, want usage", code)
	}

	h = newHarness(t, op, bw)
	if code := h.run("get", "--cache-ttl=-1h", "op://Private/GitHub/token"); code != exitcode.Usage {
		t.Errorf("negative --cache-ttl: exit %d, want usage", code)
	}

	h = newHarness(t, op, bw)
	file := writeFile(t, t.TempDir(), "env", "# pmp: cash-ttl=1h\n"+envFile)
	if code := h.run("env", file); code != exitcode.Usage {
		t.Errorf("typo in directive: exit %d, want usage", code)
	}
	if h.stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty", h.stdout.String())
	}
}

func TestRefreshFlag(t *testing.T) {
	op, bw := defaultBackends()
	h := newHarness(t, op, bw)
	store := h.opts.Cache.(*cache.Memory)
	_ = store.Set(ctx, cache.Entry{Ref: "op://Private/GitHub/token", Value: "stale", Expires: time.Now().Add(time.Hour)})
	if code := h.run("get", "--cache-ttl=1h", "--refresh", "op://Private/GitHub/token"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.stderr.String())
	}
	if h.stdout.String() != "gh-secret" {
		t.Errorf("stdout = %q, want the fresh value", h.stdout.String())
	}
	if e, _, _ := store.Get(ctx, "op://Private/GitHub/token"); e.Value != "gh-secret" {
		t.Errorf("cache = %+v, want it refreshed", e)
	}
}

func TestCacheUnsupported(t *testing.T) {
	op, bw := defaultBackends()
	h := newHarness(t, op, bw)
	h.opts.Cache, h.opts.NoDefaultCache = nil, true
	if code := h.run("get", "--cache-ttl=1h", "op://Private/GitHub/token"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.stderr.String())
	}
	if !strings.Contains(h.stderr.String(), "isn't supported") {
		t.Errorf("stderr = %q, want a warning", h.stderr.String())
	}

	h = newHarness(t, op, bw)
	h.opts.Cache, h.opts.NoDefaultCache = nil, true
	if code := h.run("cache", "list"); code != exitcode.Generic {
		t.Errorf("cache list: exit %d, want %d", code, exitcode.Generic)
	}
}

func TestCacheListAndClear(t *testing.T) {
	h := newHarness(t)
	store := h.opts.Cache.(*cache.Memory)
	_ = store.Set(ctx, cache.Entry{Ref: "op://a/b/c", Value: "SECRET1", Expires: time.Now().Add(time.Hour)})
	_ = store.Set(ctx, cache.Entry{Ref: "bw://x/y", Value: "SECRET2", Expires: time.Now().Add(-time.Hour)})

	if code := h.run("cache", "list"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.stderr.String())
	}
	out := h.stdout.String()
	if strings.Contains(out, "SECRET") {
		t.Fatalf("cache list printed a value: %q", out)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[1], "bw://x/y") || !strings.HasSuffix(lines[1], "expired") || !strings.HasSuffix(lines[2], "valid") {
		t.Errorf("cache list = %q", out)
	}

	h.stdout.Reset()
	if code := h.run("cache", "clear", "bw://x/y", "op://not/cached/at-all"); code != 0 {
		t.Fatalf("clear refs: exit %d: %s", code, h.stderr.String())
	}
	if es, _ := store.List(ctx); len(es) != 1 || es[0].Ref != "op://a/b/c" {
		t.Errorf("after clearing one ref: %v", es)
	}
	if code := h.run("cache", "clear"); code != 0 {
		t.Fatalf("clear: exit %d", code)
	}
	if es, _ := store.List(ctx); len(es) != 0 {
		t.Errorf("after clear: %v", es)
	}
}

// --timeout covers the cache: a hung cache doesn't hang get (it falls back
// to the backend), and a hung `cache list` exits with the timeout code.
func TestCacheTimeout(t *testing.T) {
	op, bw := defaultBackends()
	h := newHarness(t, op, bw)
	h.opts.Cache.(*cache.Memory).Block = true
	start := time.Now()
	if code := h.run("get", "--timeout=50ms", "--cache-ttl=1h", "op://Private/GitHub/token"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.stderr.String())
	}
	if h.stdout.String() != "gh-secret" || !strings.Contains(h.stderr.String(), "deadline exceeded") {
		t.Errorf("stdout = %q, stderr = %q", h.stdout.String(), h.stderr.String())
	}

	for _, args := range [][]string{{"cache", "list"}, {"cache", "clear"}, {"cache", "clear", "op://a/b/c"}} {
		h = newHarness(t, op, bw)
		h.opts.Cache.(*cache.Memory).Block = true
		if code := h.run(append([]string{"--timeout=50ms"}, args...)...); code != exitcode.Timeout {
			t.Errorf("%v: exit %d, want %d", args, code, exitcode.Timeout)
		}
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("took %s, want every cache call cut off by --timeout", d)
	}
}
