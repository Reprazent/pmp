// Package onepassword reads op:// references with the 1Password SDK (service
// account or desktop app) or, as a fallback, the op CLI.
package onepassword

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/Reprazent/pmp/internal/backend"
	"github.com/Reprazent/pmp/internal/ref"
)

const scheme = "op"

// strategy is one way of talking to 1Password.
type strategy interface {
	name() string
	resolve(ctx context.Context, refs []string) (map[string]string, error)
	status(ctx context.Context) backend.Status
}

// Backend resolves op:// references.
type Backend struct {
	// Version is passed to the SDK as integration info.
	Version string
	// Bin is the op executable, default "op".
	Bin string
	// Environ and Getenv default to the process environment.
	Environ func() []string
	Getenv  func(string) string
	// NewSDK creates an SDK client. Defaults to the real SDK; tests swap it.
	NewSDK func(ctx context.Context, auth SDKAuth, version string) (SecretsResolver, error)

	once     sync.Once
	strategy strategy
	err      error
}

// New returns a Backend using the process environment.
func New(version string) *Backend { return &Backend{Version: version} }

func (b *Backend) Scheme() string { return scheme }

func (b *Backend) getenv(k string) string {
	if b.Getenv != nil {
		return b.Getenv(k)
	}
	return os.Getenv(k)
}

func (b *Backend) bin() string {
	if b.Bin != "" {
		return b.Bin
	}
	return "op"
}

// Prepare picks the strategy (spec 5.1) and sets up the SDK client. It runs
// without --timeout because the desktop app may ask for Touch ID.
func (b *Backend) Prepare(ctx context.Context, _ []ref.Ref) error {
	b.once.Do(func() { b.strategy, b.err = b.pick(ctx) })
	return b.err
}

func (b *Backend) pick(ctx context.Context) (strategy, error) {
	settings := backend.SettingsFrom(ctx)
	newSDK := b.NewSDK
	if newSDK == nil {
		newSDK = newRealSDK
	}

	// 1. Service account.
	if token := b.getenv("OP_SERVICE_ACCOUNT_TOKEN"); token != "" {
		settings.Debugf("op: using SDK with service account")
		client, err := newSDK(ctx, SDKAuth{ServiceAccountToken: token}, b.Version)
		if err != nil {
			return nil, &backend.UnavailableError{Scheme: scheme, Reason: "service account: " + err.Error()}
		}
		return &sdkStrategy{client: client, label: "sdk, service account"}, nil
	}

	// 2. Desktop app.
	cliAvailable := b.cliAvailable()
	if account := b.getenv("PMP_OP_ACCOUNT"); account != "" {
		settings.Debugf("op: using SDK with desktop app, account %s", account)
		client, err := newSDK(ctx, SDKAuth{Account: account}, b.Version)
		if err == nil {
			return &sdkStrategy{client: client, label: "sdk, desktop app (" + account + ")"}, nil
		}
		if !cliAvailable {
			hint := "is the app installed and Settings → Developer → Integrate with other apps on?"
			if strings.Contains(err.Error(), "CGO") {
				hint = "this pmp was built without cgo; use a release build or install the op CLI"
			}
			return nil, &backend.UnavailableError{Scheme: scheme, Reason: "desktop app integration failed", Hint: hint}
		}
		settings.Warnf("op: desktop app integration failed, falling back to the op CLI: %v", err)
	}

	// 3. op CLI.
	if cliAvailable {
		settings.Debugf("op: using the op CLI")
		return &cliStrategy{bin: b.bin(), environ: b.Environ, account: b.getenv("PMP_OP_ACCOUNT")}, nil
	}

	// 4. Nothing.
	return nil, &backend.UnavailableError{Scheme: scheme, Reason: "no way to reach 1Password",
		Hint: "set PMP_OP_ACCOUNT, OP_SERVICE_ACCOUNT_TOKEN, or install the op CLI"}
}

func (b *Backend) cliAvailable() bool {
	_, err := exec.LookPath(b.bin())
	return err == nil
}

// Resolve resolves all references in one batch.
func (b *Backend) Resolve(ctx context.Context, refs []ref.Ref) (map[string]string, error) {
	if err := b.Prepare(ctx, refs); err != nil {
		return nil, err
	}
	raws := make([]string, len(refs))
	for i, r := range refs {
		raws[i] = r.Raw
	}
	return b.strategy.resolve(ctx, raws)
}

// Status reports which strategy is used and whether it works.
func (b *Backend) Status(ctx context.Context) backend.Status {
	if err := b.Prepare(ctx, nil); err != nil {
		return backend.Status{Detail: err.Error()}
	}
	st := b.strategy.status(ctx)
	st.Detail = fmt.Sprintf("%s: %s", b.strategy.name(), st.Detail)
	return st
}
