package onepassword

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	op "github.com/1password/onepassword-sdk-go"

	"github.com/Reprazent/pmp/internal/backend"
	"github.com/Reprazent/pmp/internal/ref"
)

var fakeOpBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "pmp-fakeop")
	if err != nil {
		panic(err)
	}
	fakeOpBin = filepath.Join(dir, "op")
	build := exec.Command("go", "build", "-o", fakeOpBin, "./testdata/fakeop")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		panic(err)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

var secrets = map[string]string{
	"op://Private/GitHub/token":          "gh-token",
	"op://Private/Multi/line":            "line1\nline2\n",
	"op://API keys/Anthropic/credential": "sk-ant",
	"op://Private/Weird/value":           "<<pmp:not-the-nonce:1>> {{ braces }}",
	"op://Work/AWS/key?attribute=otp":    "123456",
}

type fakeOp struct {
	dir string
	b   *Backend
	env map[string]string
}

func newFakeOp(t *testing.T, state string) *fakeOp {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "op")
	data, err := os.ReadFile(fakeOpBin)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(secrets)
	for name, content := range map[string][]byte{"op": data, "state": []byte(state), "secrets.json": raw} {
		if err := os.WriteFile(filepath.Join(dir, name), content, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	f := &fakeOp{dir: dir, env: map[string]string{"PATH": os.Getenv("PATH"), "HOME": dir, "GITHUB_TOKEN": "leak", "OP_BIOMETRIC_UNLOCK_ENABLED": "true"}}
	f.b = &Backend{
		Bin:    bin,
		Getenv: func(k string) string { return f.env[k] },
		Environ: func() []string {
			var out []string
			for k, v := range f.env {
				out = append(out, k+"="+v)
			}
			return out
		},
		NewSDK: func(context.Context, SDKAuth, string) (SecretsResolver, error) {
			t.Error("SDK used unexpectedly")
			return nil, errors.New("no sdk")
		},
	}
	return f
}

func (f *fakeOp) calls() []string {
	b, _ := os.ReadFile(filepath.Join(f.dir, "calls.log"))
	return strings.Split(strings.TrimSpace(string(b)), "\n")
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

func keys(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestCLIInjectBatch(t *testing.T) {
	f := newFakeOp(t, "signedin")
	got, err := f.b.Resolve(context.Background(), parseRefs(t, keys(secrets)...))
	if err != nil {
		t.Fatal(err)
	}
	for k, want := range secrets {
		if got[k] != want {
			t.Errorf("%s = %q, want %q", k, got[k], want)
		}
	}
	if calls := f.calls(); len(calls) != 1 || calls[0] != "inject" {
		t.Errorf("calls = %q, want a single inject", calls)
	}
	env, _ := os.ReadFile(filepath.Join(f.dir, "env.log"))
	if strings.Contains(string(env), "GITHUB_TOKEN") {
		t.Error("GITHUB_TOKEN leaked to op")
	}
	if !strings.Contains(string(env), "OP_BIOMETRIC_UNLOCK_ENABLED") {
		t.Error("OP_* variables should pass through")
	}
}

func TestCLIPerRefErrors(t *testing.T) {
	f := newFakeOp(t, "signedin")
	_, err := f.b.Resolve(context.Background(), parseRefs(t,
		"op://Private/GitHub/token", "op://Private/Nope/x", "op://Private/Ambiguous/x"))
	if !errors.Is(err, backend.ErrNotFound) || !errors.Is(err, backend.ErrAmbiguous) {
		t.Fatalf("err = %v, want not found and ambiguous", err)
	}
	var refErr *backend.RefError
	if !errors.As(err, &refErr) {
		t.Fatalf("want RefErrors, got %v", err)
	}
	if strings.Contains(err.Error(), "gh-token") {
		t.Error("error leaks a secret")
	}
	if !strings.Contains(err.Error(), "op://Private/Nope/x") || strings.Contains(err.Error(), "[ERROR]") {
		t.Errorf("err = %v", err)
	}
}

func TestCLISingleRefSkipsRead(t *testing.T) {
	f := newFakeOp(t, "signedin")
	_, err := f.b.Resolve(context.Background(), parseRefs(t, "op://Private/Nope/x"))
	if !errors.Is(err, backend.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	if calls := f.calls(); len(calls) != 1 {
		t.Errorf("calls = %q, want only inject", calls)
	}
}

func TestCLIAccountFlag(t *testing.T) {
	f := newFakeOp(t, "signedin")
	f.env["PMP_OP_ACCOUNT"] = "my"
	f.b.NewSDK = func(context.Context, SDKAuth, string) (SecretsResolver, error) {
		return nil, errors.New("requires CGO")
	}
	var warned bool
	ctx := backend.WithSettings(context.Background(), backend.Settings{Warnf: func(string, ...any) { warned = true }})
	if _, err := f.b.Resolve(ctx, parseRefs(t, "op://Private/GitHub/token")); err != nil {
		t.Fatal(err)
	}
	if calls := f.calls(); calls[0] != "--account my inject" {
		t.Errorf("calls = %q", calls)
	}
	if !warned {
		t.Error("expected a warning about falling back to the CLI")
	}
}

func TestCLIUnavailable(t *testing.T) {
	f := newFakeOp(t, "signedout")
	_, err := f.b.Resolve(context.Background(), parseRefs(t, "op://Private/GitHub/token", "op://Private/Multi/line"))
	if !errors.Is(err, backend.ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
	if len(f.calls()) != 1 {
		t.Errorf("should not retry per ref when signed out: %q", f.calls())
	}

	b := &Backend{Bin: "pmp-no-such-op", Getenv: func(string) string { return "" }}
	_, err = b.Resolve(context.Background(), parseRefs(t, "op://a/b/c"))
	if !errors.Is(err, backend.ErrUnavailable) {
		t.Fatalf("no op: err = %v", err)
	}
}

func TestCLIStatus(t *testing.T) {
	tests := map[string]struct {
		available bool
		detail    string
	}{
		"signedin":  {true, "cli: signed in as me@example.com"},
		"signedout": {true, "may prompt"},
		"none":      {false, "not signed in"},
	}
	for state, want := range tests {
		f := newFakeOp(t, state)
		st := f.b.Status(context.Background())
		if st.Available != want.available || !strings.Contains(st.Detail, want.detail) {
			t.Errorf("%s: %+v", state, st)
		}
	}
}

func TestTemplateRoundTrip(t *testing.T) {
	refs := []string{"op://a/b/c", "op://d/e/f"}
	nonce := "abc"
	tmpl := buildTemplate(nonce, refs)
	out := strings.Replace(strings.Replace(tmpl, "{{ op://a/b/c }}", "x\n<<pmp:abc:9>>", 1), "{{ op://d/e/f }}", "", 1)
	got, err := splitInjected(nonce, refs, out)
	if err != nil {
		t.Fatal(err)
	}
	if got["op://a/b/c"] != "x\n<<pmp:abc:9>>" || got["op://d/e/f"] != "" {
		t.Errorf("got %q", got)
	}
	if got, err := splitInjected(nonce, refs, out+"\n"); err != nil || got["op://a/b/c"] != "x\n<<pmp:abc:9>>" {
		t.Errorf("trailing newline: got %q, %v", got, err)
	}
	if _, err := splitInjected(nonce, refs, "garbage"); err == nil {
		t.Error("expected error for garbage output")
	}
}

type fakeSDK struct {
	resp op.ResolveAllResponse
	err  error
	got  []string
}

func (s *fakeSDK) ResolveAll(_ context.Context, refs []string) (op.ResolveAllResponse, error) {
	s.got = refs
	return s.resp, s.err
}

func TestSDK(t *testing.T) {
	ok := func(v string) op.Response[op.ResolvedReference, op.ResolveReferenceError] {
		return op.Response[op.ResolvedReference, op.ResolveReferenceError]{Content: &op.ResolvedReference{Secret: v}}
	}
	fail := func(e op.ResolveReferenceError) op.Response[op.ResolvedReference, op.ResolveReferenceError] {
		return op.Response[op.ResolvedReference, op.ResolveReferenceError]{Error: &e}
	}
	sdk := &fakeSDK{resp: op.ResolveAllResponse{IndividualResponses: map[string]op.Response[op.ResolvedReference, op.ResolveReferenceError]{
		"op://a/b/c":    ok("secret"),
		"op://a/nope/c": fail(op.ResolveReferenceError{Type: op.ResolveReferenceErrorTypeVariantItemNotFound}),
		"op://a/two/c":  fail(op.ResolveReferenceError{Type: op.ResolveReferenceErrorTypeVariantTooManyItems}),
		"op://bad/x":    fail(op.NewResolveReferenceErrorTypeVariantParsing("bad ref")),
	}}}

	var gotAuth SDKAuth
	b := &Backend{
		Bin: "pmp-no-such-op",
		Getenv: func(k string) string {
			return map[string]string{"OP_SERVICE_ACCOUNT_TOKEN": "ops_x", "PMP_OP_ACCOUNT": "ignored"}[k]
		},
		NewSDK: func(_ context.Context, auth SDKAuth, version string) (SecretsResolver, error) {
			gotAuth = auth
			return sdk, nil
		},
	}
	got, err := b.Resolve(context.Background(), parseRefs(t, "op://a/b/c"))
	if err != nil || got["op://a/b/c"] != "secret" {
		t.Fatalf("got %v, %v", got, err)
	}
	if gotAuth.ServiceAccountToken != "ops_x" || gotAuth.Account != "" {
		t.Errorf("auth = %+v, service account should win", gotAuth)
	}

	_, err = b.Resolve(context.Background(), parseRefs(t, "op://a/nope/c", "op://a/two/c", "op://bad/x", "op://missing/x"))
	for _, want := range []error{backend.ErrNotFound, backend.ErrAmbiguous, backend.ErrInvalidRef} {
		if !errors.Is(err, want) {
			t.Errorf("err = %v, want %v", err, want)
		}
	}
	if st := b.Status(context.Background()); !st.Available || !strings.Contains(st.Detail, "service account") {
		t.Errorf("status = %+v", st)
	}
}

func TestSDKDesktopApp(t *testing.T) {
	sdk := &fakeSDK{resp: op.ResolveAllResponse{IndividualResponses: map[string]op.Response[op.ResolvedReference, op.ResolveReferenceError]{
		"op://a/b/c": {Content: &op.ResolvedReference{Secret: "v"}},
	}}}
	var gotAuth SDKAuth
	b := &Backend{
		Bin:    "pmp-no-such-op",
		Getenv: func(k string) string { return map[string]string{"PMP_OP_ACCOUNT": "my.1password.com"}[k] },
		NewSDK: func(_ context.Context, auth SDKAuth, _ string) (SecretsResolver, error) {
			gotAuth = auth
			return sdk, nil
		},
	}
	if _, err := b.Resolve(context.Background(), parseRefs(t, "op://a/b/c")); err != nil {
		t.Fatal(err)
	}
	if gotAuth.Account != "my.1password.com" {
		t.Errorf("auth = %+v", gotAuth)
	}

	b = &Backend{
		Bin:    "pmp-no-such-op",
		Getenv: func(k string) string { return map[string]string{"PMP_OP_ACCOUNT": "my"}[k] },
		NewSDK: func(context.Context, SDKAuth, string) (SecretsResolver, error) {
			return nil, errors.New("app not running")
		},
	}
	_, err := b.Resolve(context.Background(), parseRefs(t, "op://a/b/c"))
	if !errors.Is(err, backend.ErrUnavailable) || !strings.Contains(err.Error(), "Integrate with other apps") {
		t.Errorf("err = %v", err)
	}
}

// TestIntegration talks to a real 1Password account. Opt in with
// PMP_INTEGRATION=1 and PMP_TEST_OP_REF=op://... (and PMP_TEST_OP_VALUE).
func TestIntegration(t *testing.T) {
	if os.Getenv("PMP_INTEGRATION") != "1" {
		t.Skip("set PMP_INTEGRATION=1 to run")
	}
	r := os.Getenv("PMP_TEST_OP_REF")
	if r == "" {
		t.Skip("set PMP_TEST_OP_REF")
	}
	got, err := New("test").Resolve(context.Background(), parseRefs(t, r))
	if err != nil {
		t.Fatal(err)
	}
	if want := os.Getenv("PMP_TEST_OP_VALUE"); want != "" && got[r] != want {
		t.Errorf("value mismatch for %s", r)
	}
}
