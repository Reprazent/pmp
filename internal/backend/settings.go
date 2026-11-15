package backend

import (
	"context"
	"time"
)

// Settings carries per-run options from the CLI to backends.
type Settings struct {
	// NoUnlock forbids prompting the user to unlock a backend.
	NoUnlock bool
	// Timeout is --timeout, for backend calls made outside Resolve (status
	// checks in Prepare).
	Timeout time.Duration
	// Warnf and Debugf print to stderr. Never pass secret values.
	Warnf  func(format string, args ...any)
	Debugf func(format string, args ...any)
}

type settingsKey struct{}

// WithSettings attaches s to ctx.
func WithSettings(ctx context.Context, s Settings) context.Context {
	return context.WithValue(ctx, settingsKey{}, s)
}

// SettingsFrom returns the settings attached to ctx, with no-op loggers when
// none were set.
func SettingsFrom(ctx context.Context) Settings {
	s, _ := ctx.Value(settingsKey{}).(Settings)
	if s.Warnf == nil {
		s.Warnf = func(string, ...any) {}
	}
	if s.Debugf == nil {
		s.Debugf = func(string, ...any) {}
	}
	return s
}
