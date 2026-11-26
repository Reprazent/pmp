package cache

import (
	"context"
	"strings"
	"testing"
	"time"
)

// ctx is the context for cache calls made directly by tests.
var ctx = context.Background()

func TestKeyHidesRef(t *testing.T) {
	k := Key("op://Private/GitHub/token")
	if len(k) != 64 || strings.Contains(k, "GitHub") {
		t.Errorf("Key = %q, want a hex sha256", k)
	}
	if Key("op://Private/GitHub/token") != k || Key("op://Private/GitHub/other") == k {
		t.Error("Key isn't a stable, distinct hash")
	}
}

func TestEncodeRoundTrip(t *testing.T) {
	in := Entry{Ref: "bw://x/y", Value: "multi\nline 'quoted' \"value\" ü", Expires: time.Unix(1_800_000_000, 0)}
	s, err := encode(in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := decode(s)
	if err != nil {
		t.Fatal(err)
	}
	if out.Ref != in.Ref || out.Value != in.Value || !out.Expires.Equal(in.Expires) {
		t.Errorf("round trip: got %+v, want %+v", out, in)
	}
	if _, err := decode("not json"); err == nil {
		t.Error("decode accepted garbage")
	}
}

func TestExpired(t *testing.T) {
	now := time.Unix(100, 0)
	if (Entry{Expires: time.Unix(101, 0)}).Expired(now) {
		t.Error("future entry reported expired")
	}
	if !(Entry{Expires: now}).Expired(now) {
		t.Error("entry expiring now reported valid")
	}
}

func TestMemory(t *testing.T) {
	m := NewMemory()
	_ = m.Set(ctx, Entry{Ref: "b", Value: "2"})
	_ = m.Set(ctx, Entry{Ref: "a", Value: "1"})
	es, _ := m.List(ctx)
	if len(es) != 2 || es[0].Ref != "a" {
		t.Errorf("List = %v, want sorted", es)
	}
	if ok, _ := m.Delete(ctx, "a"); !ok {
		t.Error("Delete existing = false")
	}
	if ok, _ := m.Delete(ctx, "a"); ok {
		t.Error("Delete missing = true")
	}
	_ = m.Clear(ctx)
	if es, _ := m.List(ctx); len(es) != 0 {
		t.Errorf("after Clear: %v", es)
	}
}
