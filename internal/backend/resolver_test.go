package backend_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Reprazent/pmp/internal/backend"
	"github.com/Reprazent/pmp/internal/backend/fake"
	"github.com/Reprazent/pmp/internal/ref"
)

func refs(t *testing.T, raws ...string) []ref.Ref {
	t.Helper()
	out := make([]ref.Ref, len(raws))
	for i, s := range raws {
		r, err := ref.Parse(s)
		if err != nil {
			t.Fatal(err)
		}
		out[i] = r
	}
	return out
}

func TestResolverGroupsAndDedupes(t *testing.T) {
	op := &fake.Backend{Name: "op", Secrets: map[string]string{"op://a/b/c": "1", "op://a/b/d": "2"}}
	bw := &fake.Backend{Name: "bw", Secrets: map[string]string{"bw://x/y": "3"}}
	r := &backend.Resolver{Registry: backend.NewRegistry(op, bw)}

	got, err := r.Resolve(context.Background(), refs(t, "op://a/b/c", "bw://x/y", "op://a/b/d", "op://a/b/c"))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"op://a/b/c": "1", "op://a/b/d": "2", "bw://x/y": "3"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if len(op.Calls) != 1 || len(op.Calls[0]) != 2 {
		t.Errorf("op calls = %v, want one batched call with 2 refs", op.Calls)
	}
}

func TestResolverAllOrNothing(t *testing.T) {
	op := &fake.Backend{Name: "op", Secrets: map[string]string{"op://a/b/c": "1"}}
	r := &backend.Resolver{Registry: backend.NewRegistry(op)}
	got, err := r.Resolve(context.Background(), refs(t, "op://a/b/c", "op://a/b/missing"))
	if !errors.Is(err, backend.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if got != nil {
		t.Errorf("got partial result %v", got)
	}
}

func TestResolverUnknownScheme(t *testing.T) {
	r := &backend.Resolver{Registry: backend.NewRegistry()}
	_, err := r.Resolve(context.Background(), refs(t, "nope://x"))
	if !errors.Is(err, backend.ErrInvalidRef) {
		t.Fatalf("err = %v, want ErrInvalidRef", err)
	}
}

func TestResolverSkipUnavailable(t *testing.T) {
	unavailable := &backend.UnavailableError{Scheme: "op", Reason: "op not installed"}
	op := &fake.Backend{Name: "op", Err: unavailable}
	bw := &fake.Backend{Name: "bw", Secrets: map[string]string{"bw://x/y": "3"}}
	reg := backend.NewRegistry(op, bw)

	t.Run("off", func(t *testing.T) {
		r := &backend.Resolver{Registry: reg}
		_, err := r.Resolve(context.Background(), refs(t, "op://a/b/c", "bw://x/y"))
		if !errors.Is(err, backend.ErrUnavailable) {
			t.Fatalf("err = %v, want ErrUnavailable", err)
		}
	})

	t.Run("on", func(t *testing.T) {
		var warnings []string
		r := &backend.Resolver{Registry: reg, SkipUnavailable: true, Warnf: func(f string, a ...any) {
			warnings = append(warnings, f)
		}}
		got, err := r.Resolve(context.Background(), refs(t, "op://a/b/c", "bw://x/y"))
		if err != nil {
			t.Fatal(err)
		}
		if want := map[string]string{"bw://x/y": "3"}; !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
		if len(warnings) != 1 {
			t.Errorf("warnings = %v", warnings)
		}
	})

	t.Run("missing secret still fails", func(t *testing.T) {
		r := &backend.Resolver{Registry: reg, SkipUnavailable: true}
		_, err := r.Resolve(context.Background(), refs(t, "op://a/b/c", "bw://x/missing"))
		if !errors.Is(err, backend.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("prepare unavailable is skipped", func(t *testing.T) {
		bw := &fake.Backend{Name: "bw", PrepareErr: &backend.UnavailableError{Scheme: "bw", Reason: "locked"}}
		r := &backend.Resolver{Registry: backend.NewRegistry(bw), SkipUnavailable: true}
		got, err := r.Resolve(context.Background(), refs(t, "bw://x/y"))
		if err != nil || len(got) != 0 {
			t.Fatalf("got %v, %v", got, err)
		}
		if len(bw.Calls) != 0 {
			t.Errorf("Resolve called after Prepare failed")
		}
	})
}

func TestResolverTimeout(t *testing.T) {
	op := &fake.Backend{Name: "op", Block: true}
	r := &backend.Resolver{Registry: backend.NewRegistry(op), Timeout: 10 * time.Millisecond}
	_, err := r.Resolve(context.Background(), refs(t, "op://a/b/c"))
	if !errors.Is(err, backend.ErrTimeout) {
		t.Fatalf("err = %v, want ErrTimeout", err)
	}
	if !strings.Contains(err.Error(), "op") {
		t.Errorf("error doesn't name the backend: %v", err)
	}
}

func TestRegistry(t *testing.T) {
	reg := backend.NewRegistry(&fake.Backend{Name: "op"}, &fake.Backend{Name: "bw"})
	if got := reg.Schemes(); !reflect.DeepEqual(got, []string{"bw", "op"}) {
		t.Errorf("Schemes = %v", got)
	}
	defer func() {
		if recover() == nil {
			t.Error("duplicate Register didn't panic")
		}
	}()
	reg.Register(&fake.Backend{Name: "op"})
}
