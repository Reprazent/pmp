// Package ref parses secret references of the form <scheme>://<path>[?<query>].
package ref

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// ErrInvalid is returned for strings that aren't references.
var ErrInvalid = errors.New("invalid reference")

// Ref is a parsed secret reference. It's comparable so it can be a map key.
type Ref struct {
	// Raw is the reference exactly as written. Backends that accept the
	// same syntax (1Password) get this unchanged.
	Raw string
	// Scheme is the part before "://", lower case, e.g. "op" or "bw".
	Scheme string
	// Path is everything between "://" and "?" (still percent-encoded).
	Path string
	// RawQuery is everything after the first "?", without the "?".
	RawQuery string
}

func (r Ref) String() string { return r.Raw }

// Query parses RawQuery.
func (r Ref) Query() (url.Values, error) {
	return url.ParseQuery(r.RawQuery)
}

// Segments splits Path on "/" and percent-decodes each segment, so "%2F"
// can be used for a literal slash inside a name.
func (r Ref) Segments() ([]string, error) {
	parts := strings.Split(r.Path, "/")
	for i, p := range parts {
		dec, err := url.PathUnescape(p)
		if err != nil {
			return nil, fmt.Errorf("%w: %s: bad escape in %q", ErrInvalid, r.Raw, p)
		}
		parts[i] = dec
	}
	return parts, nil
}

// SchemeOf returns the scheme of s if it looks like "<scheme>://...", and
// false otherwise. It doesn't validate the rest.
func SchemeOf(s string) (string, bool) {
	scheme, _, ok := strings.Cut(s, "://")
	if !ok || !validScheme(scheme) {
		return "", false
	}
	return strings.ToLower(scheme), true
}

// Parse parses s as a reference.
func Parse(s string) (Ref, error) {
	scheme, ok := SchemeOf(s)
	if !ok {
		return Ref{}, fmt.Errorf("%w: %q: expected <scheme>://<path>", ErrInvalid, s)
	}
	rest := s[len(scheme)+len("://"):]
	path, query, _ := strings.Cut(rest, "?")
	if path == "" {
		return Ref{}, fmt.Errorf("%w: %q: empty path", ErrInvalid, s)
	}
	if strings.ContainsAny(s, "\n\r\x00") {
		return Ref{}, fmt.Errorf("%w: %q: contains control characters", ErrInvalid, s)
	}
	return Ref{Raw: s, Scheme: scheme, Path: path, RawQuery: query}, nil
}

// IsReference reports whether value starts with one of the given schemes
// followed by "://". Env file values that don't are literals.
func IsReference(value string, schemes []string) bool {
	scheme, ok := SchemeOf(value)
	if !ok {
		return false
	}
	for _, s := range schemes {
		if s == scheme {
			return true
		}
	}
	return false
}

// RFC 3986: ALPHA *( ALPHA / DIGIT / "+" / "-" / "." )
func validScheme(s string) bool {
	if s == "" {
		return false
	}
	for i, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
		case i > 0 && (c >= '0' && c <= '9' || c == '+' || c == '-' || c == '.'):
		default:
			return false
		}
	}
	return true
}
