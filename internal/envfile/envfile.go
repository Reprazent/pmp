// Package envfile parses pmp's env files (a subset of dotenv) and formats
// resolved variables for shells.
package envfile

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Entry is one KEY=VALUE line.
type Entry struct {
	Key   string
	Value string
	Line  int
}

// Directives are settings given in `# pmp: key=value` comments.
type Directives struct {
	// CacheTTL is set by `# pmp: cache-ttl=<duration>`; HasCacheTTL
	// reports whether it was given at all.
	CacheTTL    time.Duration
	HasCacheTTL bool
}

// ErrSyntax is wrapped by all parse errors.
var ErrSyntax = errors.New("env file syntax error")

var keyRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ValidKey reports whether k is a valid variable name.
func ValidKey(k string) bool { return keyRE.MatchString(k) }

// Parse reads an env file. Supported syntax, one assignment per line:
//
//	# comment
//	KEY=value            # unquoted, trailing " # comment" is dropped
//	KEY='value'          # single quotes: taken literally
//	KEY="value"          # double quotes: \\ \" \n \$ escapes
//	export KEY=value     # optional export prefix
//
// There's no variable interpolation. When a key repeats, the last value wins
// and the key keeps its first position.
func Parse(r io.Reader, name string) ([]Entry, error) {
	entries, _, err := ParseWithDirectives(r, name)
	return entries, err
}

// ParseWithDirectives is Parse that also returns the `# pmp:` directives.
// An unknown or repeated directive is a syntax error.
func ParseWithDirectives(r io.Reader, name string) ([]Entry, Directives, error) {
	var entries []Entry
	var dir Directives
	index := map[string]int{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		if err := parseDirective(sc.Text(), &dir); err != nil {
			return nil, Directives{}, fmt.Errorf("%w: %s:%d: %v", ErrSyntax, name, lineNo, err)
		}
		e, ok, err := parseLine(sc.Text())
		if err != nil {
			return nil, Directives{}, fmt.Errorf("%w: %s:%d: %v", ErrSyntax, name, lineNo, err)
		}
		if !ok {
			continue
		}
		e.Line = lineNo
		if i, dup := index[e.Key]; dup {
			entries[i].Value = e.Value
			entries[i].Line = e.Line
			continue
		}
		index[e.Key] = len(entries)
		entries = append(entries, e)
	}
	if err := sc.Err(); err != nil {
		return nil, Directives{}, fmt.Errorf("reading %s: %w", name, err)
	}
	return entries, dir, nil
}

// parseDirective handles a `# pmp: key=value` comment line and ignores
// every other line.
func parseDirective(line string, dir *Directives) error {
	s := strings.TrimSpace(line)
	rest, ok := strings.CutPrefix(s, "#")
	if !ok {
		return nil
	}
	rest, ok = strings.CutPrefix(strings.TrimLeft(rest, " \t"), "pmp:")
	if !ok {
		return nil
	}
	key, val, ok := strings.Cut(strings.TrimSpace(rest), "=")
	key, val = strings.TrimSpace(key), strings.TrimSpace(val)
	if !ok {
		return fmt.Errorf("directive %q: expected # pmp: key=value", strings.TrimSpace(rest))
	}
	switch key {
	case "cache-ttl":
		if dir.HasCacheTTL {
			return errors.New("cache-ttl given more than once")
		}
		d, err := ParseTTL(val)
		if err != nil {
			return fmt.Errorf("cache-ttl: %v", err)
		}
		dir.CacheTTL, dir.HasCacheTTL = d, true
		return nil
	default:
		return fmt.Errorf("unknown directive %q (want cache-ttl)", key)
	}
}

// ParseTTL parses a cache TTL: a Go duration such as 8h or 30m, or 0.
// Negative values are rejected.
func ParseTTL(s string) (time.Duration, error) {
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q", s)
	}
	if d < 0 {
		return 0, fmt.Errorf("negative duration %q", s)
	}
	return d, nil
}

func parseLine(line string) (Entry, bool, error) {
	s := strings.TrimLeft(line, " \t")
	s = strings.TrimSuffix(s, "\r")
	if s == "" || s[0] == '#' {
		return Entry{}, false, nil
	}
	if rest, ok := strings.CutPrefix(s, "export"); ok && rest != "" && (rest[0] == ' ' || rest[0] == '\t') {
		s = strings.TrimLeft(rest, " \t")
	}
	key, rawVal, ok := strings.Cut(s, "=")
	if !ok {
		return Entry{}, false, errors.New("expected KEY=VALUE")
	}
	key = strings.TrimRight(key, " \t")
	if !ValidKey(key) {
		return Entry{}, false, fmt.Errorf("invalid variable name %q", key)
	}
	val, err := parseValue(strings.TrimLeft(rawVal, " \t"))
	if err != nil {
		return Entry{}, false, fmt.Errorf("%s: %v", key, err)
	}
	return Entry{Key: key, Value: val}, true, nil
}

func parseValue(s string) (string, error) {
	if s == "" {
		return "", nil
	}
	switch s[0] {
	case '\'':
		end := strings.IndexByte(s[1:], '\'')
		if end < 0 {
			return "", errors.New("unterminated single quote")
		}
		return s[1 : end+1], checkTrailing(s[end+2:])
	case '"':
		var b strings.Builder
		for i := 1; i < len(s); i++ {
			c := s[i]
			switch {
			case c == '"':
				return b.String(), checkTrailing(s[i+1:])
			case c == '\\' && i+1 < len(s):
				switch s[i+1] {
				case '\\', '"', '$':
					b.WriteByte(s[i+1])
					i++
				case 'n':
					b.WriteByte('\n')
					i++
				default:
					b.WriteByte(c)
				}
			default:
				b.WriteByte(c)
			}
		}
		return "", errors.New("unterminated double quote")
	default:
		// Unquoted: a '#' preceded by whitespace starts a comment.
		for i := 1; i < len(s); i++ {
			if s[i] == '#' && (s[i-1] == ' ' || s[i-1] == '\t') {
				s = s[:i]
				break
			}
		}
		return strings.TrimRight(s, " \t"), nil
	}
}

func checkTrailing(rest string) error {
	rest = strings.TrimLeft(rest, " \t")
	if rest == "" || rest[0] == '#' {
		return nil
	}
	return fmt.Errorf("unexpected text after closing quote: %q", rest)
}

// ParseFile parses the env file at path, including its directives.
func ParseFile(path string) ([]Entry, Directives, error) {
	f, err := os.Open(path) // #nosec G304 -- reading the user's chosen file is the point
	if err != nil {
		return nil, Directives{}, err
	}
	defer f.Close()
	return ParseWithDirectives(f, path)
}

// DefaultFile is the env file used when nothing else is given, relative to
// the home directory.
const DefaultFile = ".authenticated-env"

// Discover picks the env file: explicit (a flag or argument), then
// $PMP_ENV_FILE, then ~/.authenticated-env.
func Discover(explicit string, getenv func(string) string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if p := getenv("PMP_ENV_FILE"); p != "" {
		return p, nil
	}
	home := getenv("HOME")
	if home == "" {
		var err error
		if home, err = os.UserHomeDir(); err != nil {
			return "", fmt.Errorf("finding env file: %w", err)
		}
	}
	return filepath.Join(home, DefaultFile), nil
}
