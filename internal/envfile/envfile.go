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
)

// Entry is one KEY=VALUE line.
type Entry struct {
	Key   string
	Value string
	Line  int
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
	var entries []Entry
	index := map[string]int{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		e, ok, err := parseLine(sc.Text())
		if err != nil {
			return nil, fmt.Errorf("%w: %s:%d: %v", ErrSyntax, name, lineNo, err)
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
		return nil, fmt.Errorf("reading %s: %w", name, err)
	}
	return entries, nil
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

// ParseFile parses the env file at path.
func ParseFile(path string) ([]Entry, error) {
	f, err := os.Open(path) // #nosec G304 -- reading the user's chosen file is the point
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return Parse(f, path)
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
