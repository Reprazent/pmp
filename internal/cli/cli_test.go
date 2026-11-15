package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Reprazent/pmp/internal/backend"
	"github.com/Reprazent/pmp/internal/backend/fake"
	"github.com/Reprazent/pmp/internal/exitcode"
)

type harness struct {
	opts   *Options
	stdout bytes.Buffer
	stderr bytes.Buffer
	env    map[string]string
	exec   struct {
		called bool
		argv0  string
		argv   []string
		env    []string
	}
}

func newHarness(t *testing.T, backends ...backend.Backend) *harness {
	t.Helper()
	h := &harness{env: map[string]string{"HOME": t.TempDir()}}
	h.opts = &Options{
		Registry: backend.NewRegistry(backends...),
		Stdout:   &h.stdout,
		Stderr:   &h.stderr,
		Getenv:   func(k string) string { return h.env[k] },
		Environ:  func() []string { return []string{"PATH=/bin", "GITHUB_TOKEN=old"} },
		Exec: func(argv0 string, argv []string, env []string) error {
			h.exec.called = true
			h.exec.argv0, h.exec.argv, h.exec.env = argv0, argv, env
			return nil
		},
	}
	return h
}

func (h *harness) run(args ...string) int {
	root := NewRootCommand(BuildInfo{Version: "test"}, h.opts)
	root.SetArgs(args)
	root.SetContext(context.Background())
	return exitcode.For(Execute(root))
}

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func defaultBackends() (*fake.Backend, *fake.Backend) {
	op := &fake.Backend{Name: "op", Secrets: map[string]string{
		"op://Private/GitHub/token": "gh-secret",
		"op://Private/Quote/value":  "it's",
	}}
	bw := &fake.Backend{Name: "bw", Secrets: map[string]string{
		"bw://Anthropic/password": "sk-ant",
	}}
	return op, bw
}

const envFile = `GITHUB_TOKEN="op://Private/GitHub/token"
ANTHROPIC_API_KEY="bw://Anthropic/password"
EDITOR=nvim
URL=https://example.com
QUOTE=op://Private/Quote/value
`

func TestGet(t *testing.T) {
	op, bw := defaultBackends()

	h := newHarness(t, op, bw)
	if code := h.run("get", "op://Private/GitHub/token"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.stderr.String())
	}
	if h.stdout.String() != "gh-secret" {
		t.Errorf("stdout = %q", h.stdout.String())
	}

	h = newHarness(t, op, bw)
	h.run("get", "-n", "bw://Anthropic/password")
	if h.stdout.String() != "sk-ant\n" {
		t.Errorf("stdout with -n = %q", h.stdout.String())
	}
}

func TestGetErrors(t *testing.T) {
	op, bw := defaultBackends()
	tests := []struct {
		args []string
		want int
	}{
		{[]string{"get"}, exitcode.Usage},
		{[]string{"get", "a", "b"}, exitcode.Usage},
		{[]string{"get", "nvim"}, exitcode.Usage},
		{[]string{"get", "nope://x/y"}, exitcode.Usage},
		{[]string{"get", "op://Private/Missing/x"}, exitcode.NotFound},
		{[]string{"nonsense"}, exitcode.Usage},
		{[]string{"get", "--bogus", "op://a/b"}, exitcode.Usage},
	}
	for _, tt := range tests {
		h := newHarness(t, op, bw)
		if got := h.run(tt.args...); got != tt.want {
			t.Errorf("%v: exit %d, want %d (stderr %q)", tt.args, got, tt.want, h.stderr.String())
		}
		if h.stdout.Len() != 0 {
			t.Errorf("%v: stdout = %q, want empty", tt.args, h.stdout.String())
		}
	}
}

func TestEnv(t *testing.T) {
	op, bw := defaultBackends()
	h := newHarness(t, op, bw)
	file := writeFile(t, t.TempDir(), "env", envFile)

	if code := h.run("env", file); code != 0 {
		t.Fatalf("exit %d: %s", code, h.stderr.String())
	}
	want := `export GITHUB_TOKEN='gh-secret'
export ANTHROPIC_API_KEY='sk-ant'
export EDITOR='nvim'
export URL='https://example.com'
export QUOTE='it'\''s'
`
	if h.stdout.String() != want {
		t.Errorf("stdout:\n%s\nwant:\n%s", h.stdout.String(), want)
	}
}

func TestEnvJSON(t *testing.T) {
	op, bw := defaultBackends()
	h := newHarness(t, op, bw)
	file := writeFile(t, t.TempDir(), "env", envFile)
	if code := h.run("env", "--format=json", file); code != 0 {
		t.Fatalf("exit %d: %s", code, h.stderr.String())
	}
	var got map[string]string
	if err := json.Unmarshal(h.stdout.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["GITHUB_TOKEN"] != "gh-secret" || got["QUOTE"] != "it's" {
		t.Errorf("json = %v", got)
	}
}

func TestEnvDiscovery(t *testing.T) {
	op, bw := defaultBackends()

	h := newHarness(t, op, bw)
	writeFile(t, h.env["HOME"], ".authenticated-env", "A=default\n")
	h.run("env")
	if h.stdout.String() != "export A='default'\n" {
		t.Errorf("default file: %q", h.stdout.String())
	}

	h = newHarness(t, op, bw)
	writeFile(t, h.env["HOME"], ".authenticated-env", "A=default\n")
	h.env["PMP_ENV_FILE"] = writeFile(t, t.TempDir(), "custom", "A=custom\n")
	h.run("env")
	if h.stdout.String() != "export A='custom'\n" {
		t.Errorf("PMP_ENV_FILE: %q", h.stdout.String())
	}

	h = newHarness(t, op, bw)
	if code := h.run("env"); code != exitcode.Usage {
		t.Errorf("missing default file: exit %d", code)
	}
}

func TestEnvAllOrNothing(t *testing.T) {
	op, bw := defaultBackends()
	h := newHarness(t, op, bw)
	file := writeFile(t, t.TempDir(), "env", envFile+"MISSING=op://Private/Nope/x\n")
	if code := h.run("env", file); code != exitcode.NotFound {
		t.Errorf("exit %d", code)
	}
	if h.stdout.Len() != 0 {
		t.Errorf("printed partial output: %q", h.stdout.String())
	}
}

func TestEnvSkipUnavailable(t *testing.T) {
	op, _ := defaultBackends()
	bw := &fake.Backend{Name: "bw", Err: &backend.UnavailableError{Scheme: "bw", Reason: "bw is not installed"}}
	file := writeFile(t, t.TempDir(), "env", envFile)

	h := newHarness(t, op, bw)
	if code := h.run("env", file); code != exitcode.Unavailable {
		t.Errorf("without flag: exit %d", code)
	}

	h = newHarness(t, op, bw)
	if code := h.run("env", "--skip-unavailable", file); code != 0 {
		t.Fatalf("with flag: exit %d: %s", code, h.stderr.String())
	}
	if strings.Contains(h.stdout.String(), "ANTHROPIC") {
		t.Errorf("skipped var was printed: %q", h.stdout.String())
	}
	if !strings.Contains(h.stdout.String(), "GITHUB_TOKEN='gh-secret'") {
		t.Errorf("missing resolved var: %q", h.stdout.String())
	}
	if !strings.Contains(h.stderr.String(), "warning") {
		t.Errorf("no warning: %q", h.stderr.String())
	}

	h = newHarness(t, op, bw)
	h.run("-q", "env", "--skip-unavailable", file)
	if h.stderr.Len() != 0 {
		t.Errorf("--quiet still warned: %q", h.stderr.String())
	}
}

func TestEnvErrors(t *testing.T) {
	op, bw := defaultBackends()
	dir := t.TempDir()
	tests := []struct {
		name string
		args []string
		want int
	}{
		{"syntax", []string{"env", writeFile(t, dir, "bad", "NOPE\n")}, exitcode.Usage},
		{"format", []string{"env", "--format=fish", writeFile(t, dir, "ok", "A=1\n")}, exitcode.Usage},
		{"too many args", []string{"env", "a", "b"}, exitcode.Usage},
	}
	for _, tt := range tests {
		h := newHarness(t, op, bw)
		if got := h.run(tt.args...); got != tt.want {
			t.Errorf("%s: exit %d, want %d", tt.name, got, tt.want)
		}
	}
}

func TestRun(t *testing.T) {
	op, bw := defaultBackends()
	h := newHarness(t, op, bw)
	file := writeFile(t, t.TempDir(), "env", envFile)

	if code := h.run("run", "--env-file", file, "--", "sh", "-c", "echo hi", "--env-file"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.stderr.String())
	}
	if !h.exec.called {
		t.Fatal("exec not called")
	}
	if want := []string{"sh", "-c", "echo hi", "--env-file"}; !reflect.DeepEqual(h.exec.argv, want) {
		t.Errorf("argv = %q, want %q", h.exec.argv, want)
	}
	if filepath.Base(h.exec.argv0) != "sh" {
		t.Errorf("argv0 = %q", h.exec.argv0)
	}
	want := []string{
		"PATH=/bin",
		"GITHUB_TOKEN=gh-secret",
		"ANTHROPIC_API_KEY=sk-ant",
		"EDITOR=nvim",
		"URL=https://example.com",
		"QUOTE=it's",
	}
	if !reflect.DeepEqual(h.exec.env, want) {
		t.Errorf("env = %q, want %q", h.exec.env, want)
	}
	if h.stdout.Len() != 0 {
		t.Errorf("run printed to stdout: %q", h.stdout.String())
	}
}

func TestRunWithoutDash(t *testing.T) {
	op, bw := defaultBackends()
	h := newHarness(t, op, bw)
	h.env["PMP_ENV_FILE"] = writeFile(t, t.TempDir(), "env", "A=1\n")
	if code := h.run("run", "sh", "-c", "true"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.stderr.String())
	}
	if want := []string{"sh", "-c", "true"}; !reflect.DeepEqual(h.exec.argv, want) {
		t.Errorf("argv = %q", h.exec.argv)
	}
}

func TestRunErrors(t *testing.T) {
	op, bw := defaultBackends()
	file := writeFile(t, t.TempDir(), "env", "A=op://Private/Nope/x\n")
	ok := writeFile(t, t.TempDir(), "env", "A=1\n")
	tests := []struct {
		name string
		args []string
		want int
	}{
		{"no command", []string{"run", "--env-file", ok}, exitcode.Usage},
		{"missing secret", []string{"run", "--env-file", file, "--", "true"}, exitcode.NotFound},
		{"unknown command", []string{"run", "--env-file", ok, "--", "definitely-not-a-command-pmp"}, exitcode.Usage},
	}
	for _, tt := range tests {
		h := newHarness(t, op, bw)
		if got := h.run(tt.args...); got != tt.want {
			t.Errorf("%s: exit %d, want %d (%s)", tt.name, got, tt.want, h.stderr.String())
		}
		if h.exec.called {
			t.Errorf("%s: exec called", tt.name)
		}
	}
}
