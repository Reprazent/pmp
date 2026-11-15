package envfile

import (
	"bytes"
	"errors"
	"os/exec"
	"reflect"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	in := `# comments allowed
GITHUB_TOKEN="op://Personal/GitHub/token"
AWS_ACCESS_KEY_ID="bw://AWS/access_key_id?folder=Work"
EDITOR="nvim"                        # plain values pass through unchanged
export FOO='op://Vault/Item/field'   # optional export prefix

  INDENTED=yes
UNQUOTED=a b#c # comment
EMPTY=
EMPTY_QUOTED=""
SINGLE='it''s'
ESCAPES="a\"b\\c\nd\$e\x"
LITERAL='a\nb $HOME'
SPACED = value
exportX=1
GITHUB_TOKEN=override
CRLF=value` + "\r\n"

	got, err := Parse(strings.NewReader(in), "test.env")
	if err == nil {
		t.Fatalf("expected error for SINGLE='it''s', got %v", got)
	}

	in = strings.Replace(in, "SINGLE='it''s'\n", "", 1)
	got, err = Parse(strings.NewReader(in), "test.env")
	if err != nil {
		t.Fatal(err)
	}
	want := []Entry{
		{"GITHUB_TOKEN", "override", 15},
		{"AWS_ACCESS_KEY_ID", "bw://AWS/access_key_id?folder=Work", 3},
		{"EDITOR", "nvim", 4},
		{"FOO", "op://Vault/Item/field", 5},
		{"INDENTED", "yes", 7},
		{"UNQUOTED", "a b#c", 8},
		{"EMPTY", "", 9},
		{"EMPTY_QUOTED", "", 10},
		{"ESCAPES", "a\"b\\c\nd$e\\x", 11},
		{"LITERAL", `a\nb $HOME`, 12},
		{"SPACED", "value", 13},
		{"exportX", "1", 14},
		{"CRLF", "value", 16},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got:\n%#v\nwant:\n%#v", got, want)
	}
}

func TestParseErrors(t *testing.T) {
	for _, in := range []string{
		"NOEQUALS",
		"1BAD=x",
		"BAD-KEY=x",
		`OPEN="abc`,
		`OPEN='abc`,
		`TRAIL="abc" junk`,
		"=value",
	} {
		_, err := Parse(strings.NewReader("OK=1\n"+in+"\n"), "f")
		if !errors.Is(err, ErrSyntax) {
			t.Errorf("Parse(%q) err = %v, want ErrSyntax", in, err)
			continue
		}
		if !strings.Contains(err.Error(), "f:2:") {
			t.Errorf("Parse(%q) err = %v, want line number", in, err)
		}
	}
}

func TestDiscover(t *testing.T) {
	env := map[string]string{"HOME": "/home/me"}
	getenv := func(k string) string { return env[k] }

	if got, _ := Discover("/explicit", getenv); got != "/explicit" {
		t.Errorf("explicit: %q", got)
	}
	if got, _ := Discover("", getenv); got != "/home/me/.authenticated-env" {
		t.Errorf("default: %q", got)
	}
	env["PMP_ENV_FILE"] = "/from/env"
	if got, _ := Discover("", getenv); got != "/from/env" {
		t.Errorf("PMP_ENV_FILE: %q", got)
	}
	if got, _ := Discover("/explicit", getenv); got != "/explicit" {
		t.Errorf("explicit beats PMP_ENV_FILE: %q", got)
	}
}

func TestWrite(t *testing.T) {
	entries := []Entry{{Key: "A", Value: "it's"}, {Key: "B", Value: "x\"$y\n"}}
	tests := map[string]string{
		"sh":     "export A='it'\\''s'\nexport B='x\"$y\n'\n",
		"dotenv": "A=\"it's\"\nB=\"x\\\"\\$y\\n\"\n",
		"json":   "{\n  \"A\": \"it's\",\n  \"B\": \"x\\\"$y\\n\"\n}\n",
	}
	for format, want := range tests {
		var b bytes.Buffer
		if err := Write(&b, format, entries); err != nil {
			t.Fatal(err)
		}
		if b.String() != want {
			t.Errorf("%s:\ngot  %q\nwant %q", format, b.String(), want)
		}
	}
	var b bytes.Buffer
	if err := Write(&b, "json", nil); err != nil || b.String() != "{}\n" {
		t.Errorf("empty json = %q, %v", b.String(), err)
	}
	if err := Write(&b, "fish", entries); err == nil {
		t.Error("fish format should be unknown")
	}
}

// TestShRoundTrip evals the sh output in a real shell.
func TestShRoundTrip(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	values := []string{"", "plain", "it's", `'"'`, "$HOME `id` $(id)", "a\nb\n", "\\", "tab\there", "ünïcödé", "-n", "'''"}
	for _, v := range values {
		var b bytes.Buffer
		if err := Write(&b, "sh", []Entry{{Key: "V", Value: v}}); err != nil {
			t.Fatal(err)
		}
		out, err := exec.Command(sh, "-c", b.String()+`printf %s "$V"`).Output()
		if err != nil {
			t.Fatalf("sh failed for %q: %v", v, err)
		}
		if string(out) != v {
			t.Errorf("round trip %q: got %q", v, out)
		}
	}
}

func FuzzShQuote(f *testing.F) {
	for _, s := range []string{"", "'", "a'b", "'''", "\x00"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		q := ShQuote(s)
		// Undo the quoting the way a POSIX shell would.
		if !strings.HasPrefix(q, "'") || !strings.HasSuffix(q, "'") {
			t.Fatalf("not single-quoted: %q", q)
		}
		got := strings.ReplaceAll(q[1:len(q)-1], `'\''`, "'")
		if got != s {
			t.Fatalf("ShQuote(%q) = %q, unquotes to %q", s, q, got)
		}
		if strings.Count(strings.ReplaceAll(q[1:len(q)-1], `'\''`, ""), "'") != 0 {
			t.Fatalf("unescaped quote in %q", q)
		}
	})
}

func FuzzParse(f *testing.F) {
	f.Add("A=1\nexport B='x'\nC=\"y\\n\" # c\n")
	f.Add("# only a comment")
	f.Add(`K="\\\""`)
	f.Fuzz(func(t *testing.T, s string) {
		entries, err := Parse(strings.NewReader(s), "fuzz")
		if err != nil {
			return
		}
		for _, e := range entries {
			if !ValidKey(e.Key) {
				t.Fatalf("invalid key %q", e.Key)
			}
		}
	})
}

// FuzzDotenvRoundTrip checks Parse(Write(dotenv)) returns the same values.
func FuzzDotenvRoundTrip(f *testing.F) {
	for _, s := range []string{"", "a\"b", "x\\ny", "$HOME", "a\nb", "# not a comment"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, v string) {
		if strings.ContainsAny(v, "\r") {
			t.Skip("carriage returns aren't supported in values")
		}
		var b bytes.Buffer
		if err := Write(&b, "dotenv", []Entry{{Key: "K", Value: v}}); err != nil {
			t.Fatal(err)
		}
		got, err := Parse(&b, "fuzz")
		if err != nil {
			t.Fatalf("Parse(Write(%q)): %v", v, err)
		}
		if len(got) != 1 || got[0].Value != v {
			t.Fatalf("round trip %q: got %#v", v, got)
		}
	})
}
