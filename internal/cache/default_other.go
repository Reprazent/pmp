//go:build !darwin

package cache

// Default returns the platform's store, or nil when there isn't one. Only
// macOS has a store for now.
func Default() Store { return nil }
