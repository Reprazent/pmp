package main

import (
	"github.com/Reprazent/pmp/internal/backend"
	"github.com/Reprazent/pmp/internal/backend/bitwarden"
)

// registry lists the backends pmp ships with. Adding a backend means adding
// one line here.
func registry() *backend.Registry {
	return backend.NewRegistry(
		bitwarden.New(),
	)
}
