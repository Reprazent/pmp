package cache

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// Service is the Keychain service pmp's items live under.
const Service = "pmp"

const (
	// maxCommand is the longest line `security -i` accepts.
	maxCommand = 4096
	// base64Prefix marks data written by pmp (and by go-keyring, which
	// pmp used before). security hex-encodes data that isn't plain ASCII
	// on output, so everything is stored base64-encoded.
	base64Prefix = "go-keyring-base64:"
	hexPrefix    = "go-keyring-encoded:"
)

// securityPath is the security binary. Tests swap it for a fake.
var securityPath = "/usr/bin/security"

// errNotFound is what security reports for a missing item.
var errNotFound = errors.New("item not found")

// Keychain stores entries as generic password items in the default (login)
// Keychain. The account is Key(ref), the data is the JSON-encoded entry.
//
// It runs /usr/bin/security for every call, killing it when the context is
// done, so a Keychain stuck behind an unlock dialog can't hang pmp.
type Keychain struct {
	// Service defaults to "pmp". Tests use a different one.
	Service string
}

// Default returns the platform's store, or nil when there isn't one.
func Default() Store { return &Keychain{Service: Service} }

func (k *Keychain) service() string {
	if k.Service == "" {
		return Service
	}
	return k.Service
}

// run runs security with args (and stdin, if not empty) and returns its
// combined output. Once ctx is done, the process is killed and ctx.Err()
// returned.
func run(ctx context.Context, stdin string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, securityPath, args...)
	cmd.WaitDelay = time.Second
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	out, err := cmd.CombinedOutput()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, fmt.Errorf("keychain: %w", ctxErr)
	}
	if err != nil {
		if bytes.Contains(out, []byte("could not be found")) {
			return nil, errNotFound
		}
		return nil, fmt.Errorf("keychain: %s %w: %s", args[0], err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

func (k *Keychain) get(ctx context.Context, account string) (string, error) {
	out, err := run(ctx, "", "find-generic-password", "-s", k.service(), "-a", account, "-w")
	if err != nil {
		return "", err
	}
	s := strings.TrimSpace(string(out))
	switch {
	case strings.HasPrefix(s, base64Prefix):
		b, err := base64.StdEncoding.DecodeString(s[len(base64Prefix):])
		return string(b), err
	case strings.HasPrefix(s, hexPrefix):
		b, err := hex.DecodeString(s[len(hexPrefix):])
		return string(b), err
	}
	return s, nil
}

func (k *Keychain) Get(ctx context.Context, ref string) (Entry, bool, error) {
	data, err := k.get(ctx, Key(ref))
	if errors.Is(err, errNotFound) {
		return Entry{}, false, nil
	}
	if err != nil {
		return Entry{}, false, err
	}
	e, err := decode(data)
	if err != nil {
		return Entry{}, false, err
	}
	if e.Ref != ref {
		// Not ours (or a hash collision): treat it as a miss.
		return Entry{}, false, nil
	}
	return e, true, nil
}

func (k *Keychain) Set(ctx context.Context, e Entry) error {
	data, err := encode(e)
	if err != nil {
		return err
	}
	// Service, account and base64 data only contain [A-Za-z0-9+/=:_-],
	// so quoting them for security's tokenizer is plain single quotes. The
	// command goes in on stdin so the value never shows up in argv.
	command := fmt.Sprintf("add-generic-password -U -s '%s' -a '%s' -w '%s'\n",
		k.service(), Key(e.Ref), base64Prefix+base64.StdEncoding.EncodeToString([]byte(data)))
	if len(command) > maxCommand {
		return ErrTooBig
	}
	if strings.Contains(k.service(), "'") {
		return fmt.Errorf("keychain: invalid service %q", k.service())
	}
	_, err = run(ctx, command, "-i")
	return err
}

func (k *Keychain) Delete(ctx context.Context, ref string) (bool, error) {
	_, err := run(ctx, "", "delete-generic-password", "-s", k.service(), "-a", Key(ref))
	if errors.Is(err, errNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (k *Keychain) Clear(ctx context.Context) error {
	// Delete one item at a time until there are none left.
	for {
		_, err := run(ctx, "", "delete-generic-password", "-s", k.service())
		if errors.Is(err, errNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func (k *Keychain) List(ctx context.Context) ([]Entry, error) {
	// dump-keychain without -d prints item attributes only, which doesn't
	// prompt. The data is then read item by item.
	out, err := run(ctx, "", "dump-keychain")
	if err != nil {
		return nil, err
	}
	var entries []Entry
	for _, acct := range accounts(out, k.service()) {
		data, err := k.get(ctx, acct)
		if errors.Is(err, errNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		e, err := decode(data)
		if err != nil || Key(e.Ref) != acct {
			continue // not an item pmp wrote
		}
		entries = append(entries, e)
	}
	sortEntries(entries)
	return entries, nil
}

var attrRE = regexp.MustCompile(`^\s*"(svce|acct)"<blob>="(.*)"$`)

// accounts returns the accounts of generic password items for service in
// `security dump-keychain` output.
func accounts(dump []byte, service string) []string {
	var out []string
	var svce, acct string
	var genp bool
	flush := func() {
		if genp && svce == service && acct != "" {
			out = append(out, acct)
		}
		svce, acct, genp = "", "", false
	}
	sc := bufio.NewScanner(bytes.NewReader(dump))
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "keychain: ") {
			flush()
			continue
		}
		if strings.HasPrefix(line, "class: ") {
			genp = strings.TrimSpace(strings.TrimPrefix(line, "class: ")) == `"genp"`
			continue
		}
		if m := attrRE.FindStringSubmatch(line); m != nil {
			if m[1] == "svce" {
				svce = m[2]
			} else {
				acct = m[2]
			}
		}
	}
	flush()
	return out
}
