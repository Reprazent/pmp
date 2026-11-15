package ref

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		in      string
		want    Ref
		wantErr bool
	}{
		{in: "op://Personal/GitHub/token", want: Ref{Raw: "op://Personal/GitHub/token", Scheme: "op", Path: "Personal/GitHub/token"}},
		{in: "op://Work/AWS/credentials/key?attribute=otp", want: Ref{Raw: "op://Work/AWS/credentials/key?attribute=otp", Scheme: "op", Path: "Work/AWS/credentials/key", RawQuery: "attribute=otp"}},
		{in: "BW://GitHub/password", want: Ref{Raw: "BW://GitHub/password", Scheme: "bw", Path: "GitHub/password"}},
		{in: "secret-service://a=b", want: Ref{Raw: "secret-service://a=b", Scheme: "secret-service", Path: "a=b"}},
		{in: "bw://API keys/Anthropic/password", want: Ref{Raw: "bw://API keys/Anthropic/password", Scheme: "bw", Path: "API keys/Anthropic/password"}},
		{in: "op://", wantErr: true},
		{in: "op://?x=y", wantErr: true},
		{in: "nvim", wantErr: true},
		{in: "://foo", wantErr: true},
		{in: "1p://foo", wantErr: true},
		{in: "op:/foo", wantErr: true},
		{in: "op://a\nb", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := Parse(tt.in)
			if tt.wantErr {
				if !errors.Is(err, ErrInvalid) {
					t.Fatalf("Parse(%q) err = %v, want ErrInvalid", tt.in, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse(%q): %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("Parse(%q) = %+v, want %+v", tt.in, got, tt.want)
			}
		})
	}
}

func TestSegments(t *testing.T) {
	r, err := Parse("bw://AC%2FDC/pass%20word?folder=Work")
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.Segments()
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"AC/DC", "pass word"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Segments = %q, want %q", got, want)
	}

	bad, _ := Parse("bw://a%zz/b")
	if _, err := bad.Segments(); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad escape: err = %v", err)
	}
}

func TestIsReference(t *testing.T) {
	schemes := []string{"op", "bw"}
	tests := map[string]bool{
		"op://a/b/c":        true,
		"OP://a/b/c":        true,
		"bw://a/b":          true,
		"https://example":   false,
		"nvim":              false,
		"op:/a":             false,
		" op://a/b/c":       false,
		"keychain://a/b":    false,
		"":                  false,
		"postgres://u:p@h/": false,
	}
	for in, want := range tests {
		if got := IsReference(in, schemes); got != want {
			t.Errorf("IsReference(%q) = %v, want %v", in, got, want)
		}
	}
}

func FuzzParse(f *testing.F) {
	for _, s := range []string{"op://a/b/c", "bw://x/y?id=1", "a://", "://", "x", "op://a%2F/b"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		r, err := Parse(s)
		if err != nil {
			return
		}
		if r.Raw != s {
			t.Fatalf("Raw = %q, want %q", r.Raw, s)
		}
		if r.Path == "" || r.Scheme == "" {
			t.Fatalf("empty part in %+v", r)
		}
		if !strings.EqualFold(s[:len(r.Scheme)], r.Scheme) {
			t.Fatalf("scheme %q not a prefix of %q", r.Scheme, s)
		}
		_, _ = r.Segments()
		_, _ = r.Query()
	})
}
