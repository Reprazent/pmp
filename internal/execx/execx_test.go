package execx

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestFilter(t *testing.T) {
	environ := []string{
		"PATH=/bin", "HOME=/h", "XDG_CONFIG_HOME=/c", "GITHUB_TOKEN=secret",
		"BW_SESSION=old", "OP_ACCOUNT=me", "OPX=no", "AWS_SECRET_ACCESS_KEY=x",
	}
	got := Filter(environ, []string{"BW_SESSION", "OP_*"}, []string{"BW_SESSION=new"})
	want := []string{"PATH=/bin", "HOME=/h", "XDG_CONFIG_HOME=/c", "OP_ACCOUNT=me", "BW_SESSION=new"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Filter = %q, want %q", got, want)
	}
}

func TestRun(t *testing.T) {
	ctx := context.Background()
	env := func() []string { return []string{"PATH=/usr/bin:/bin", "LEAK=1"} }

	res, err := Run(ctx, Cmd{Name: "sh", Args: []string{"-c", `printf '%s' "${LEAK:-none} $EXTRA"`}, Environ: env, Env: []string{"EXTRA=x"}})
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Stdout) != "none x" {
		t.Errorf("stdout = %q", res.Stdout)
	}

	_, err = Run(ctx, Cmd{Name: "sh", Args: []string{"-c", "echo oops >&2; exit 3"}, Environ: env})
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode != 3 || !strings.Contains(exitErr.Error(), "oops") {
		t.Errorf("err = %v", err)
	}

	_, err = Run(ctx, Cmd{Name: "pmp-definitely-missing", Environ: env})
	if !errors.Is(err, ErrNotInstalled) {
		t.Errorf("err = %v, want ErrNotInstalled", err)
	}

	tctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	_, err = Run(tctx, Cmd{Name: "sleep", Args: []string{"5"}, Environ: env})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want DeadlineExceeded", err)
	}
}
