package onepassword

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/Reprazent/pmp/internal/backend"
	"github.com/Reprazent/pmp/internal/execx"
)

// cliStrategy uses the op CLI: one `op inject` for the whole batch, and
// `op read` per reference only to explain a failed batch.
type cliStrategy struct {
	bin     string
	environ func() []string
	account string
}

func (c *cliStrategy) name() string { return "cli" }

func (c *cliStrategy) run(ctx context.Context, stdin string, args ...string) (execx.Result, error) {
	if c.account != "" {
		args = append([]string{"--account", c.account}, args...)
	}
	cmd := execx.Cmd{
		Name:    c.bin,
		Args:    args,
		Allow:   []string{"OP_*", "XDG_*"},
		Environ: c.environ,
	}
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	res, err := execx.Run(ctx, cmd)
	return res, classify(err)
}

// classify maps op CLI failures onto backend errors.
func classify(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, execx.ErrNotInstalled) {
		return &backend.UnavailableError{Scheme: scheme, Reason: "op is not installed"}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%s: %w", scheme, backend.ErrTimeout)
	}
	var exitErr *execx.ExitError
	if !errors.As(err, &exitErr) {
		return fmt.Errorf("%s: %w", scheme, err)
	}
	msg := strings.TrimSpace(exitErr.Stderr)
	lower := strings.ToLower(msg)
	contains := func(subs ...string) bool {
		for _, s := range subs {
			if strings.Contains(lower, s) {
				return true
			}
		}
		return false
	}
	switch {
	case contains("not currently signed in", "is not signed in", "no accounts configured",
		"account is not signed in", "authorization prompt dismissed", "connecting to desktop app", "session expired"):
		return &backend.UnavailableError{Scheme: scheme, Reason: firstLine(msg), Hint: "run `op signin`"}
	case contains("more than one", "multiple items", "multiple vaults", "multiple fields"):
		return fmt.Errorf("%w: %s", backend.ErrAmbiguous, firstLine(msg))
	case contains("isn't an item", "isn't a vault", "isn't a field", "could not find", "no item found",
		"not found", "does not have a field", "doesn't have a field", "isn't a section"):
		return fmt.Errorf("%w: %s", backend.ErrNotFound, firstLine(msg))
	case contains("invalid secret reference", "invalid character", "too few", "too many"):
		return fmt.Errorf("%w: %s", backend.ErrInvalidRef, firstLine(msg))
	}
	return fmt.Errorf("%s: %s", scheme, firstLine(msg))
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	// op prefixes errors with "[ERROR] 2024/01/02 15:04:05 ".
	if rest, ok := strings.CutPrefix(s, "[ERROR] "); ok {
		if f := strings.SplitN(rest, " ", 3); len(f) == 3 {
			return f[2]
		}
		return rest
	}
	return s
}

func (c *cliStrategy) resolve(ctx context.Context, refs []string) (map[string]string, error) {
	nonce, err := newNonce()
	if err != nil {
		return nil, err
	}
	tmpl := buildTemplate(nonce, refs)
	res, err := c.run(ctx, tmpl, "inject")
	if err == nil {
		return splitInjected(nonce, refs, string(res.Stdout))
	}
	if errors.Is(err, backend.ErrUnavailable) || errors.Is(err, backend.ErrTimeout) || len(refs) == 1 {
		if len(refs) == 1 {
			return nil, &backend.RefError{Ref: refs[0], Err: err}
		}
		return nil, err
	}
	// op inject fails the whole batch on one bad reference; find out which.
	return c.readEach(ctx, refs)
}

func (c *cliStrategy) readEach(ctx context.Context, refs []string) (map[string]string, error) {
	out := map[string]string{}
	errs := make([]error, len(refs))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i, r := range refs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := c.run(ctx, "", "read", "--no-newline", r)
			if err != nil {
				errs[i] = &backend.RefError{Ref: r, Err: err}
				return
			}
			mu.Lock()
			out[r] = string(res.Stdout)
			mu.Unlock()
		}()
	}
	wg.Wait()
	var multi backend.MultiError
	for _, e := range errs {
		if e != nil {
			multi = append(multi, e)
		}
	}
	return out, multi.ErrOrNil()
}

func newNonce() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func marker(nonce string, i int) string { return fmt.Sprintf("<<pmp:%s:%d>>", nonce, i) }

// buildTemplate wraps each reference in random markers so the injected
// output can be split back up even when secrets contain newlines.
func buildTemplate(nonce string, refs []string) string {
	var b strings.Builder
	for i, r := range refs {
		b.WriteString(marker(nonce, i))
		b.WriteString("{{ ")
		b.WriteString(r)
		b.WriteString(" }}")
	}
	b.WriteString(marker(nonce, len(refs)))
	return b.String()
}

func splitInjected(nonce string, refs []string, out string) (map[string]string, error) {
	values := make(map[string]string, len(refs))
	rest := out
	for i := range refs {
		start, next := marker(nonce, i), marker(nonce, i+1)
		after, ok := strings.CutPrefix(rest, start)
		if !ok {
			return nil, fmt.Errorf("%s: unexpected `op inject` output", scheme)
		}
		end := strings.Index(after, next)
		if end < 0 {
			return nil, fmt.Errorf("%s: unexpected `op inject` output", scheme)
		}
		values[refs[i]] = after[:end]
		rest = after[end:]
	}
	// op inject (2.34.0 at least) appends a newline the template didn't have.
	rest = strings.TrimRight(rest, "\r\n")
	if rest != marker(nonce, len(refs)) {
		return nil, fmt.Errorf("%s: unexpected `op inject` output", scheme)
	}
	return values, nil
}

func (c *cliStrategy) status(ctx context.Context) backend.Status {
	res, err := c.run(ctx, "", "whoami", "--format", "json")
	if err == nil {
		var who struct {
			Email string `json:"email"`
			URL   string `json:"url"`
		}
		if json.Unmarshal(res.Stdout, &who) == nil && who.Email != "" {
			return backend.Status{Available: true, Detail: "signed in as " + who.Email}
		}
		return backend.Status{Available: true, Detail: "signed in"}
	}
	// With desktop app integration op signs in on first use, so configured
	// accounts are good enough.
	res, lerr := c.run(ctx, "", "account", "list", "--format", "json")
	if lerr == nil {
		var accounts []json.RawMessage
		if json.Unmarshal(res.Stdout, &accounts) == nil && len(accounts) > 0 {
			return backend.Status{Available: true, Detail: fmt.Sprintf("%d account(s), not signed in yet (may prompt)", len(accounts))}
		}
	}
	return backend.Status{Detail: err.Error()}
}
