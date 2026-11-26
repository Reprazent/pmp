// Package bitwarden reads bw:// references through the Bitwarden CLI (bw).
package bitwarden

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/Reprazent/pmp/internal/backend"
	"github.com/Reprazent/pmp/internal/execx"
	"github.com/Reprazent/pmp/internal/ref"
)

const scheme = "bw"

const sessionHint = "run `export BW_SESSION=$(bw unlock --raw)`"

// TTY is the terminal pmp prompts on. *os.File satisfies it.
type TTY interface {
	io.ReadWriter
	Close() error
}

// Backend resolves bw:// references with the bw CLI.
type Backend struct {
	// Bin is the bw executable, default "bw".
	Bin string
	// Environ and Getenv default to the process environment.
	Environ func() []string
	Getenv  func(string) string
	// OpenTTY opens the controlling terminal for the unlock prompt.
	// Defaults to /dev/tty.
	OpenTTY func() (TTY, error)

	once    sync.Once
	session string
	prepErr error
}

// New returns a Backend using the process environment.
func New() *Backend { return &Backend{} }

func (b *Backend) Scheme() string { return scheme }

func (b *Backend) bin() string {
	if b.Bin != "" {
		return b.Bin
	}
	return "bw"
}

func (b *Backend) getenv(k string) string {
	if b.Getenv != nil {
		return b.Getenv(k)
	}
	return os.Getenv(k)
}

func (b *Backend) openTTY() (TTY, error) {
	if b.OpenTTY != nil {
		return b.OpenTTY()
	}
	return os.OpenFile("/dev/tty", os.O_RDWR, 0)
}

// run calls bw with the session (if any) and the env vars bw needs.
func (b *Backend) run(ctx context.Context, session string, args ...string) (execx.Result, error) {
	cmd := execx.Cmd{
		Name:    b.bin(),
		Args:    append(args, "--nointeraction"),
		Allow:   []string{"BITWARDENCLI_*", "NODE_EXTRA_CA_CERTS", "APPDATA"},
		Environ: b.Environ,
	}
	if session != "" {
		cmd.Env = []string{"BW_SESSION=" + session}
	}
	res, err := execx.Run(ctx, cmd)
	return res, b.classify(err)
}

func (b *Backend) classify(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, execx.ErrNotInstalled):
		return &backend.UnavailableError{Scheme: scheme, Reason: "bw is not installed"}
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("%s: %w", scheme, backend.ErrTimeout)
	}
	var exitErr *execx.ExitError
	if errors.As(err, &exitErr) {
		msg := strings.ToLower(exitErr.Stderr)
		switch {
		case strings.Contains(msg, "vault is locked"):
			return &backend.UnavailableError{Scheme: scheme, Reason: "vault is locked", Hint: sessionHint}
		case strings.Contains(msg, "not logged in"), strings.Contains(msg, "you are not logged in"):
			return &backend.UnavailableError{Scheme: scheme, Reason: "not logged in", Hint: "run `bw login`"}
		}
	}
	return fmt.Errorf("%s: %w", scheme, err)
}

type status struct {
	Status    string `json:"status"`
	UserEmail string `json:"userEmail"`
}

func (b *Backend) status(ctx context.Context, session string) (status, error) {
	res, err := b.run(ctx, session, "status")
	if err != nil {
		return status{}, err
	}
	var st status
	if err := json.Unmarshal(res.Stdout, &st); err != nil {
		return status{}, fmt.Errorf("%s: parsing `bw status`: %w", scheme, err)
	}
	return st, nil
}

// Status reports whether bw can be used, without prompting.
func (b *Backend) Status(ctx context.Context) backend.Status {
	st, err := b.status(ctx, b.getenv("BW_SESSION"))
	if err != nil {
		return backend.Status{Detail: err.Error()}
	}
	switch st.Status {
	case "unlocked":
		return backend.Status{Available: true, Detail: "unlocked as " + st.UserEmail}
	case "locked":
		if backend.SettingsFrom(ctx).NoUnlock {
			return backend.Status{Detail: "locked; --no-unlock is set (" + sessionHint + ")"}
		}
		if tty, err := b.openTTY(); err == nil {
			_ = tty.Close()
			return backend.Status{Available: true, Detail: "locked as " + st.UserEmail + "; pmp will prompt to unlock"}
		}
		return backend.Status{Detail: "locked and no terminal to prompt on (" + sessionHint + ")"}
	case "unauthenticated":
		return backend.Status{Detail: "not logged in (run `bw login`)"}
	default:
		return backend.Status{Detail: "unknown status " + st.Status}
	}
}

// Prepare makes sure there's an unlocked session, prompting on the terminal
// if needed. It runs once per process, so all references share one prompt.
func (b *Backend) Prepare(ctx context.Context, _ []ref.Ref) error {
	b.once.Do(func() { b.session, b.prepErr = b.unlock(ctx) })
	return b.prepErr
}

func (b *Backend) unlock(ctx context.Context) (string, error) {
	settings := backend.SettingsFrom(ctx)
	session := b.getenv("BW_SESSION")

	statusCtx := ctx
	if settings.Timeout > 0 {
		var cancel context.CancelFunc
		statusCtx, cancel = context.WithTimeout(ctx, settings.Timeout)
		defer cancel()
	}
	st, err := b.status(statusCtx, session)
	if err != nil {
		return "", err
	}
	switch st.Status {
	case "unlocked":
		return session, nil
	case "unauthenticated":
		return "", &backend.UnavailableError{Scheme: scheme, Reason: "not logged in", Hint: "run `bw login`"}
	case "locked":
	default:
		return "", fmt.Errorf("%s: unexpected status %q", scheme, st.Status)
	}

	reason := "vault is locked"
	if session != "" {
		reason = "vault is locked and BW_SESSION is invalid or expired"
	}
	if settings.NoUnlock {
		return "", &backend.UnavailableError{Scheme: scheme, Reason: reason + " and --no-unlock is set", Hint: sessionHint}
	}
	tty, err := b.openTTY()
	if err != nil {
		return "", &backend.UnavailableError{Scheme: scheme, Reason: reason + " and there's no terminal to prompt on", Hint: sessionHint}
	}
	defer tty.Close()

	settings.Debugf("bw: prompting to unlock")
	// Unlock waits for a human, so it doesn't get --timeout. The prompt and
	// any errors go to the terminal; only the session key comes back on
	// stdout. No --nointeraction here, so call execx directly.
	res, err := execx.Run(ctx, execx.Cmd{
		Name:    b.bin(),
		Args:    []string{"unlock", "--raw"},
		Allow:   []string{"BITWARDENCLI_*", "NODE_EXTRA_CA_CERTS", "APPDATA"},
		Environ: b.Environ,
		Stdin:   tty,
		Stderr:  tty,
	})
	if err != nil {
		return "", fmt.Errorf("%s: unlock failed: %w", scheme, b.classify(err))
	}
	key := strings.TrimSpace(string(res.Stdout))
	if key == "" {
		return "", fmt.Errorf("%s: unlock returned no session", scheme)
	}
	return key, nil
}

// Cacheable reports whether r's value can be cached. TOTP codes change every
// 30 seconds, so they can't.
func (b *Backend) Cacheable(r ref.Ref) bool {
	q, err := parseRef(r)
	return err == nil && q.field != fieldTOTP
}
