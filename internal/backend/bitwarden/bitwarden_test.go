package bitwarden

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Reprazent/pmp/internal/backend"
	"github.com/Reprazent/pmp/internal/ref"
)

const itemsJSON = `[
  {"id":"gh","name":"GitHub","folderId":null,"organizationId":null,"collectionIds":[],"notes":"some notes",
   "login":{"username":"me","password":"gh-pass","totp":"otpauth://totp/x?secret=GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ",
            "uris":[{"uri":"https://github.com"},{"uri":"https://gist.github.com"}]},
   "fields":[{"name":"token","value":"gh-token","type":1},{"name":"login","value":null,"type":3,"linkedId":100}]},
  {"id":"aws-work","name":"AWS","folderId":"f-work","organizationId":null,"collectionIds":[],"notes":null,
   "login":{"username":"work","password":"work-pass","totp":null,"uris":[]},
   "fields":[{"name":"access_key_id","value":"AKIAWORK","type":0}]},
  {"id":"aws-home","name":"AWS","folderId":"f-home","organizationId":null,"collectionIds":[],"notes":null,
   "login":{"username":"home","password":"home-pass","totp":null,"uris":[]},
   "fields":[{"name":"access_key_id","value":"AKIAHOME","type":0},{"name":"dup","value":"1","type":0},{"name":"dup","value":"2","type":0}]},
  {"id":"steam","name":"Steam","folderId":null,"organizationId":null,"collectionIds":[],"notes":null,
   "login":{"username":"s","password":"p","totp":"steam://ABCDEF","uris":[]},"fields":[]},
  {"id":"note","name":"A/B note","folderId":"f-nested","organizationId":null,"collectionIds":[],"notes":"secret note",
   "login":null,"fields":null},
  {"id":"shared-a","name":"Shared","folderId":null,"organizationId":"org-a","collectionIds":["c-a"],"notes":null,
   "login":{"username":null,"password":"shared-a","totp":null,"uris":[]},"fields":[]},
  {"id":"shared-b","name":"Shared","folderId":null,"organizationId":"org-b","collectionIds":["c-b"],"notes":null,
   "login":{"username":null,"password":"shared-b","totp":null,"uris":[]},"fields":[]}
]`

const foldersJSON = `[{"id":"f-work","name":"Work"},{"id":"f-home","name":"Home"},{"id":"f-nested","name":"Work/Deep"}]`
const collectionsJSON = `[{"id":"c-a","name":"Infra","organizationId":"org-a"},{"id":"c-b","name":"Infra","organizationId":"org-b"}]`
const orgsJSON = `[{"id":"org-a","name":"Acme"},{"id":"org-b","name":"Beta"}]`

type fakeBW struct {
	dir     string
	backend *Backend
	env     map[string]string
	warns   []string
}

func newFakeBW(t *testing.T, status string) *fakeBW {
	t.Helper()
	dir := t.TempDir()
	script, err := os.ReadFile("testdata/bw")
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"bw": string(script), "status": status, "items.json": itemsJSON, "folders.json": foldersJSON,
		"collections.json": collectionsJSON, "organizations.json": orgsJSON,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	f := &fakeBW{dir: dir, env: map[string]string{"PATH": os.Getenv("PATH"), "HOME": dir, "GITHUB_TOKEN": "leak"}}
	f.backend = &Backend{
		Bin: filepath.Join(dir, "bw"),
		Environ: func() []string {
			var out []string
			for k, v := range f.env {
				out = append(out, k+"="+v)
			}
			return out
		},
		Getenv:  func(k string) string { return f.env[k] },
		OpenTTY: func() (TTY, error) { return nil, errors.New("no tty") },
	}
	return f
}

func (f *fakeBW) ctx(s backend.Settings) context.Context {
	s.Warnf = func(format string, args ...any) { f.warns = append(f.warns, format) }
	return backend.WithSettings(context.Background(), s)
}

func (f *fakeBW) calls(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(f.dir, "calls.log"))
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

func (f *fakeBW) lastEnv(t *testing.T) string {
	t.Helper()
	b, _ := os.ReadFile(filepath.Join(f.dir, "env.log"))
	return string(b)
}

func parseRefs(t *testing.T, raws ...string) []ref.Ref {
	t.Helper()
	out := make([]ref.Ref, len(raws))
	for i, s := range raws {
		r, err := ref.Parse(s)
		if err != nil {
			t.Fatal(err)
		}
		out[i] = r
	}
	return out
}

func TestResolveFields(t *testing.T) {
	f := newFakeBW(t, "unlocked")
	tests := map[string]string{
		"bw://GitHub/password":                           "gh-pass",
		"bw://GitHub/username":                           "me",
		"bw://GitHub/notes":                              "some notes",
		"bw://GitHub/uri":                                "https://github.com",
		"bw://GitHub/token":                              "gh-token",
		"bw://GitHub/login":                              "me",
		"bw://AWS/access_key_id?folder=Work":             "AKIAWORK",
		"bw://AWS/password?folder=Home":                  "home-pass",
		"bw://A%2FB note/notes?folder=Work%2FDeep":       "secret note",
		"bw://Shared/password?collection=Infra&org=Beta": "shared-b",
		"bw://Shared/password?org=Acme":                  "shared-a",
		"bw://AWS/password?id=aws-work":                  "work-pass",
		"bw://Steam/totp":                                "123456",
	}
	var raws []string
	for k := range tests {
		raws = append(raws, k)
	}
	got, err := f.backend.Resolve(f.ctx(backend.Settings{}), parseRefs(t, raws...))
	if err != nil {
		t.Fatal(err)
	}
	for k, want := range tests {
		if got[k] != want {
			t.Errorf("%s = %q, want %q", k, got[k], want)
		}
	}
	if len(f.warns) != 0 {
		t.Errorf("unexpected warnings %v", f.warns)
	}
}

func TestResolveTOTP(t *testing.T) {
	f := newFakeBW(t, "unlocked")
	got, err := f.backend.Resolve(f.ctx(backend.Settings{}), parseRefs(t, "bw://GitHub/totp"))
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^\d{6}$`).MatchString(got["bw://GitHub/totp"]) {
		t.Errorf("totp = %q", got["bw://GitHub/totp"])
	}
	for _, c := range f.calls(t) {
		if strings.HasPrefix(c, "get totp") {
			t.Errorf("otpauth TOTP should be computed locally, called bw %s", c)
		}
	}
}

func TestResolveBatchesAndMinimalEnv(t *testing.T) {
	f := newFakeBW(t, "unlocked")
	_, err := f.backend.Resolve(f.ctx(backend.Settings{}), parseRefs(t, "bw://GitHub/password", "bw://GitHub/token", "bw://Old AWS/password?id=aws-home"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"status --nointeraction", "list items --nointeraction"}
	if got := f.calls(t); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("calls = %q, want %q", got, want)
	}
	if env := f.lastEnv(t); strings.Contains(env, "GITHUB_TOKEN") {
		t.Errorf("GITHUB_TOKEN leaked into bw's environment")
	}
	if len(f.warns) != 1 || !strings.Contains(f.warns[0], "is called") {
		t.Errorf("want rename warning for id label, got %v", f.warns)
	}
}

func TestResolveErrors(t *testing.T) {
	f := newFakeBW(t, "unlocked")
	tests := map[string]error{
		"bw://GitHub":                 backend.ErrInvalidRef,
		"bw://a/b/c":                  backend.ErrInvalidRef,
		"bw://GitHub/password?nope=1": backend.ErrInvalidRef,
		"bw://GitHub/password?folder=a&collection=b":  backend.ErrInvalidRef,
		"bw://GitHub/password?id=x&folder=a":          backend.ErrInvalidRef,
		"bw://Missing/password":                       backend.ErrNotFound,
		"bw://GitHub/nope":                            backend.ErrNotFound,
		"bw://AWS/totp?folder=Work":                   backend.ErrNotFound,
		"bw://AWS/password":                           backend.ErrAmbiguous,
		"bw://AWS/dup?folder=Home":                    backend.ErrAmbiguous,
		"bw://Shared/password?collection=Infra":       backend.ErrAmbiguous,
		"bw://AWS/password?folder=Nope":               backend.ErrNotFound,
		"bw://GitHub/password?id=nope":                backend.ErrNotFound,
		"bw://A%2FB note/password?folder=Work%2FDeep": backend.ErrNotFound,
	}
	for raw, want := range tests {
		_, err := f.backend.Resolve(f.ctx(backend.Settings{}), parseRefs(t, raw))
		if !errors.Is(err, want) {
			t.Errorf("%s: err = %v, want %v", raw, err, want)
		}
	}

	_, err := f.backend.Resolve(f.ctx(backend.Settings{}), parseRefs(t, "bw://AWS/password"))
	if !strings.Contains(err.Error(), "aws-work") || !strings.Contains(err.Error(), "aws-home") {
		t.Errorf("ambiguous error should list ids: %v", err)
	}
}

type fakeTTY struct {
	in  *strings.Reader
	out bytes.Buffer
}

func (f *fakeTTY) Read(p []byte) (int, error)  { return f.in.Read(p) }
func (f *fakeTTY) Write(p []byte) (int, error) { return f.out.Write(p) }
func (f *fakeTTY) Close() error                { return nil }

func TestUnlock(t *testing.T) {
	t.Run("session from env", func(t *testing.T) {
		f := newFakeBW(t, "locked")
		f.env["BW_SESSION"] = "good-session"
		if _, err := f.backend.Resolve(f.ctx(backend.Settings{}), parseRefs(t, "bw://GitHub/password")); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(f.lastEnv(t), "BW_SESSION=good-session") {
			t.Error("BW_SESSION not passed to bw")
		}
	})

	t.Run("prompts once on tty", func(t *testing.T) {
		f := newFakeBW(t, "locked")
		tty := &fakeTTY{in: strings.NewReader("hunter2\n")}
		opened := 0
		f.backend.OpenTTY = func() (TTY, error) { opened++; return tty, nil }
		ctx := f.ctx(backend.Settings{})
		refs := parseRefs(t, "bw://GitHub/password", "bw://GitHub/username")
		if err := f.backend.Prepare(ctx, refs); err != nil {
			t.Fatal(err)
		}
		got, err := f.backend.Resolve(ctx, refs)
		if err != nil {
			t.Fatal(err)
		}
		if got["bw://GitHub/password"] != "gh-pass" {
			t.Errorf("got %v", got)
		}
		if opened != 1 {
			t.Errorf("opened tty %d times", opened)
		}
		if !strings.Contains(tty.out.String(), "Master password") {
			t.Errorf("prompt didn't go to the tty: %q", tty.out.String())
		}
		unlocks := 0
		for _, c := range f.calls(t) {
			if strings.HasPrefix(c, "unlock") {
				unlocks++
				if strings.Contains(c, "nointeraction") {
					t.Error("unlock must be interactive")
				}
			}
		}
		if unlocks != 1 {
			t.Errorf("unlock called %d times", unlocks)
		}
	})

	t.Run("wrong password", func(t *testing.T) {
		f := newFakeBW(t, "locked")
		f.backend.OpenTTY = func() (TTY, error) { return &fakeTTY{in: strings.NewReader("nope\n")}, nil }
		_, err := f.backend.Resolve(f.ctx(backend.Settings{}), parseRefs(t, "bw://GitHub/password"))
		if err == nil || errors.Is(err, backend.ErrUnavailable) {
			t.Errorf("err = %v, want a generic failure", err)
		}
	})

	unavailable := []struct {
		name   string
		status string
		s      backend.Settings
		hint   string
	}{
		{"no tty", "locked", backend.Settings{}, "BW_SESSION"},
		{"no-unlock", "locked", backend.Settings{NoUnlock: true}, "--no-unlock"},
		{"logged out", "unauthenticated", backend.Settings{}, "bw login"},
	}
	for _, tt := range unavailable {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeBW(t, tt.status)
			f.backend.OpenTTY = func() (TTY, error) {
				if tt.s.NoUnlock {
					t.Error("opened tty despite --no-unlock")
				}
				return nil, errors.New("no tty")
			}
			_, err := f.backend.Resolve(f.ctx(tt.s), parseRefs(t, "bw://GitHub/password"))
			if !errors.Is(err, backend.ErrUnavailable) {
				t.Fatalf("err = %v, want ErrUnavailable", err)
			}
			if !strings.Contains(err.Error(), tt.hint) {
				t.Errorf("err = %v, want hint %q", err, tt.hint)
			}
		})
	}

	t.Run("not installed", func(t *testing.T) {
		b := &Backend{Bin: "pmp-no-such-bw"}
		_, err := b.Resolve(context.Background(), parseRefs(t, "bw://GitHub/password"))
		if !errors.Is(err, backend.ErrUnavailable) {
			t.Errorf("err = %v", err)
		}
	})
}

func TestStatus(t *testing.T) {
	tests := []struct {
		status    string
		tty       bool
		noUnlock  bool
		available bool
		detail    string
	}{
		{"unlocked", false, false, true, "unlocked as me@example.com"},
		{"locked", true, false, true, "will prompt"},
		{"locked", false, false, false, "no terminal"},
		{"locked", true, true, false, "--no-unlock"},
		{"unauthenticated", true, false, false, "not logged in"},
	}
	for _, tt := range tests {
		f := newFakeBW(t, tt.status)
		f.backend.OpenTTY = func() (TTY, error) {
			if !tt.tty {
				return nil, errors.New("no tty")
			}
			return &fakeTTY{in: strings.NewReader("")}, nil
		}
		st := f.backend.Status(f.ctx(backend.Settings{NoUnlock: tt.noUnlock}))
		if st.Available != tt.available || !strings.Contains(st.Detail, tt.detail) {
			t.Errorf("%s tty=%v noUnlock=%v: %+v", tt.status, tt.tty, tt.noUnlock, st)
		}
	}

	st := (&Backend{Bin: "pmp-no-such-bw"}).Status(context.Background())
	if st.Available || !strings.Contains(st.Detail, "not installed") {
		t.Errorf("missing bw: %+v", st)
	}
}

func TestCacheable(t *testing.T) {
	b := New()
	for raw, want := range map[string]bool{
		"bw://GitHub/password":      true,
		"bw://GitHub/token?id=abc":  true,
		"bw://GitHub/totp":          false,
		"bw://GitHub/totp?org=Acme": false,
		"bw://only-one-segment":     false,
	} {
		if got := b.Cacheable(parseRefs(t, raw)[0]); got != want {
			t.Errorf("Cacheable(%s) = %v, want %v", raw, got, want)
		}
	}
}
