// Package execx runs backend CLIs with a minimal environment.
package execx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// ErrNotInstalled is returned when the binary isn't on PATH.
var ErrNotInstalled = errors.New("not installed")

// baseEnv is passed to every backend CLI. Anything else has to be allowed
// explicitly per backend, so secrets in the user's environment (other
// tokens, for example) don't leak into tools that don't need them.
var baseEnv = []string{
	"PATH", "HOME", "USER", "LOGNAME", "SHELL", "TMPDIR", "TERM",
	"LANG", "LC_ALL", "LC_CTYPE", "LC_MESSAGES", "TZ",
	"SSL_CERT_FILE", "SSL_CERT_DIR",
	"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy",
}

// basePrefixes are allowed by prefix.
var basePrefixes = []string{"XDG_"}

// Cmd describes one CLI invocation.
type Cmd struct {
	Name string
	Args []string
	// Allow lists extra environment variables (exact names, or prefixes
	// ending in "*") passed through from Environ.
	Allow []string
	// Env adds variables (KEY=VALUE), overriding Environ.
	Env []string
	// Environ is the environment to filter. Defaults to os.Environ.
	Environ func() []string
	// Stdin and Stderr default to nothing and a capture buffer. Setting
	// Stderr (e.g. to a TTY) means Result.Stderr stays empty.
	Stdin  io.Reader
	Stderr io.Writer
}

// Result is what a command produced. Stdout may contain secrets: never log
// it.
type Result struct {
	Stdout   []byte
	Stderr   string
	ExitCode int
}

// ExitError is returned when the command ran but exited non-zero.
type ExitError struct {
	Name     string
	ExitCode int
	Stderr   string
}

func (e *ExitError) Error() string {
	msg := strings.TrimSpace(e.Stderr)
	if msg == "" {
		msg = "no output"
	}
	return fmt.Sprintf("%s exited with %d: %s", e.Name, e.ExitCode, msg)
}

// Run runs c. It returns ErrNotInstalled if the binary isn't found, the
// context's error if it was cancelled, and *ExitError for non-zero exits.
func Run(ctx context.Context, c Cmd) (Result, error) {
	path, err := exec.LookPath(c.Name)
	if err != nil {
		return Result{}, fmt.Errorf("%s: %w", c.Name, ErrNotInstalled)
	}
	environ := c.Environ
	if environ == nil {
		environ = os.Environ
	}

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, path, c.Args...)
	cmd.Env = Filter(environ(), c.Allow, c.Env)
	cmd.Stdin = c.Stdin
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if c.Stderr != nil {
		cmd.Stderr = c.Stderr
	}
	err = cmd.Run()
	res := Result{Stdout: stdout.Bytes(), Stderr: stderr.String()}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return res, ctxErr
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		res.ExitCode = exitErr.ExitCode()
		return res, &ExitError{Name: c.Name, ExitCode: res.ExitCode, Stderr: res.Stderr}
	}
	return res, err
}

// Filter keeps the base variables plus allow from environ, then applies
// extra on top.
func Filter(environ []string, allow []string, extra []string) []string {
	var out []string
	override := map[string]bool{}
	for _, kv := range extra {
		k, _, _ := strings.Cut(kv, "=")
		override[k] = true
	}
	for _, kv := range environ {
		k, _, _ := strings.Cut(kv, "=")
		if override[k] || !allowed(k, allow) {
			continue
		}
		out = append(out, kv)
	}
	return append(out, extra...)
}

func allowed(k string, allow []string) bool {
	for _, a := range baseEnv {
		if k == a {
			return true
		}
	}
	for _, p := range basePrefixes {
		if strings.HasPrefix(k, p) {
			return true
		}
	}
	for _, a := range allow {
		if p, ok := strings.CutSuffix(a, "*"); ok {
			if strings.HasPrefix(k, p) {
				return true
			}
		} else if k == a {
			return true
		}
	}
	return false
}
