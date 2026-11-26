// Package cache stores resolved secrets for a limited time so repeat calls
// don't have to go through slow password manager CLIs. See SPEC 5.5.
package cache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Entry is one cached value.
type Entry struct {
	Ref     string    `json:"ref"`
	Value   string    `json:"value"`
	Expires time.Time `json:"-"`
}

// Expired reports whether e has expired at now.
func (e Entry) Expired(now time.Time) bool { return !now.Before(e.Expires) }

// ErrTooBig is returned by Set when a value doesn't fit in the store.
var ErrTooBig = errors.New("value too big to cache")

// Store keeps entries keyed by their raw reference.
type Store interface {
	// Get returns the entry for ref, expired or not. ok is false when
	// there's no entry.
	Get(ctx context.Context, ref string) (e Entry, ok bool, err error)
	// Set adds or replaces the entry for e.Ref.
	Set(ctx context.Context, e Entry) error
	// Delete removes the entry for ref. ok is false when there was none.
	Delete(ctx context.Context, ref string) (ok bool, err error)
	// List returns every entry, sorted by ref.
	List(ctx context.Context) ([]Entry, error)
	// Clear removes every entry.
	Clear(ctx context.Context) error
}

// Every Store method must return promptly with ctx.Err() once ctx is done.

// Key is the store key for ref: a hex SHA-256, so the reference itself is
// only kept inside the encrypted data.
func Key(ref string) string {
	sum := sha256.Sum256([]byte(ref))
	return hex.EncodeToString(sum[:])
}

type wireEntry struct {
	Ref     string `json:"ref"`
	Value   string `json:"value"`
	Expires int64  `json:"expires"`
}

func encode(e Entry) (string, error) {
	b, err := json.Marshal(wireEntry{Ref: e.Ref, Value: e.Value, Expires: e.Expires.Unix()})
	return string(b), err
}

func decode(s string) (Entry, error) {
	var w wireEntry
	if err := json.Unmarshal([]byte(s), &w); err != nil {
		return Entry{}, fmt.Errorf("decoding cache entry: %w", err)
	}
	return Entry{Ref: w.Ref, Value: w.Value, Expires: time.Unix(w.Expires, 0)}, nil
}

type Memory struct {
	mu      sync.Mutex
	entries map[string]Entry
	// Err, when set, is returned from every call.
	Err error
	// Block, when set, makes every call wait until its context is done,
	// like a Keychain stuck behind an unlock dialog.
	Block bool
}

func (m *Memory) enter(ctx context.Context) error {
	if m.Block {
		<-ctx.Done()
		return ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return m.Err
}

func NewMemory() *Memory { return &Memory{entries: map[string]Entry{}} }

func (m *Memory) Get(ctx context.Context, ref string) (Entry, bool, error) {
	if err := m.enter(ctx); err != nil {
		return Entry{}, false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.entries[ref]
	return e, ok, nil
}

func (m *Memory) Set(ctx context.Context, e Entry) error {
	if err := m.enter(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries[e.Ref] = e
	return nil
}

func (m *Memory) Delete(ctx context.Context, ref string) (bool, error) {
	if err := m.enter(ctx); err != nil {
		return false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.entries[ref]
	delete(m.entries, ref)
	return ok, nil
}

func (m *Memory) List(ctx context.Context) ([]Entry, error) {
	if err := m.enter(ctx); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Entry, 0, len(m.entries))
	for _, e := range m.entries {
		out = append(out, e)
	}
	sortEntries(out)
	return out, nil
}

func (m *Memory) Clear(ctx context.Context) error {
	if err := m.enter(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries = map[string]Entry{}
	return nil
}

func sortEntries(es []Entry) {
	sort.Slice(es, func(i, j int) bool { return es[i].Ref < es[j].Ref })
}
