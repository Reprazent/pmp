package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/Reprazent/pmp/internal/backend"
	"github.com/Reprazent/pmp/internal/envfile"
	"github.com/Reprazent/pmp/internal/exitcode"
	"github.com/Reprazent/pmp/internal/ref"
)

func newGetCommand(opts *Options) *cobra.Command {
	var newline bool
	cmd := &cobra.Command{
		Use:   "get <reference>",
		Short: "Print one secret to stdout",
		Long: `Print one secret to stdout, without a trailing newline unless -n is given.
Only the secret goes to stdout; everything else goes to stderr.`,
		Example: `  pmp get op://Private/GitHub/token
  curl -H "Authorization: Bearer $(pmp get bw://GitHub/password)" ...`,
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := ref.Parse(args[0])
			if err != nil {
				return usageErr(err)
			}
			res, err := opts.resolver(false, envfile.Directives{})
			if err != nil {
				return err
			}
			values, err := res.Resolve(cmd.Context(), []ref.Ref{r})
			if err != nil {
				return err
			}
			out := values[r.Raw]
			if newline {
				out += "\n"
			}
			_, err = io.WriteString(opts.Stdout, out)
			return err
		},
	}
	cmd.Flags().BoolVarP(&newline, "newline", "n", false, "print a trailing newline")
	return cmd
}

func newEnvCommand(opts *Options) *cobra.Command {
	var format string
	var skip bool
	cmd := &cobra.Command{
		Use:   "env [file]",
		Short: "Print an env file with references resolved, for eval",
		Long: `Resolve every reference in an env file and print the variables.

The file defaults to $PMP_ENV_FILE, then ~/.authenticated-env. Resolution is
all or nothing: if any reference fails, nothing is printed.`,
		Example: `  eval "$(pmp env ~/.authenticated-env)"
  pmp env --format=json | jq .`,
		Args: maxArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !validFormat(format) {
				return usagef("unknown --format %q (want one of %s)", format, strings.Join(envfile.Formats, ", "))
			}
			var file string
			if len(args) == 1 {
				file = args[0]
			}
			entries, err := opts.resolveEnvFile(cmd.Context(), file, skip)
			if err != nil {
				return err
			}
			// Format into a buffer first so a write error can't leave half
			// the output behind.
			var buf bytes.Buffer
			if err := envfile.Write(&buf, format, entries); err != nil {
				return err
			}
			_, err = buf.WriteTo(opts.Stdout)
			return err
		},
	}
	cmd.Flags().StringVar(&format, "format", "sh", "output format: "+strings.Join(envfile.Formats, ", "))
	cmd.Flags().BoolVar(&skip, "skip-unavailable", false, "skip references whose backend is missing or locked, with a warning")
	return cmd
}

func newRunCommand(opts *Options) *cobra.Command {
	var file string
	var skip bool
	cmd := &cobra.Command{
		Use:   "run [--env-file <file>] -- <command> [args...]",
		Short: "Run a command with the env file's secrets in its environment",
		Long: `Resolve every reference in an env file and exec the command with the
variables added to its environment. Secrets never touch stdout.

The file defaults to $PMP_ENV_FILE, then ~/.authenticated-env.`,
		Example: `  pmp run -- terraform plan
  pmp run --env-file ./deploy.env --skip-unavailable -- ./deploy.sh`,
		Args: minArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			entries, err := opts.resolveEnvFile(cmd.Context(), file, skip)
			if err != nil {
				return err
			}
			path, err := exec.LookPath(args[0])
			if err != nil {
				return usageErr(err)
			}
			opts.Debugf("exec %s", path)
			return opts.Exec(path, args, mergeEnv(opts.Environ(), entries))
		},
	}
	// Everything after the command name belongs to the command.
	cmd.Flags().SetInterspersed(false)
	cmd.Flags().StringVar(&file, "env-file", "", "env file (default $PMP_ENV_FILE, then ~/.authenticated-env)")
	cmd.Flags().BoolVar(&skip, "skip-unavailable", false, "skip references whose backend is missing or locked, with a warning")
	return cmd
}

// mergeEnv adds entries to base. Entries win over existing variables.
func mergeEnv(base []string, entries []envfile.Entry) []string {
	override := make(map[string]bool, len(entries))
	for _, e := range entries {
		override[e.Key] = true
	}
	out := make([]string, 0, len(base)+len(entries))
	for _, kv := range base {
		k, _, _ := strings.Cut(kv, "=")
		if !override[k] {
			out = append(out, kv)
		}
	}
	for _, e := range entries {
		out = append(out, e.Key+"="+e.Value)
	}
	return out
}

func execProcess(argv0 string, argv []string, env []string) error {
	return syscall.Exec(argv0, argv, env) // #nosec G204 -- running the user's command is the point
}

func validFormat(f string) bool {
	for _, x := range envfile.Formats {
		if f == x {
			return true
		}
	}
	return false
}

func exactArgs(n int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) != n {
			return usagef("%s: expected %d argument(s), got %d", cmd.Name(), n, len(args))
		}
		return nil
	}
}

func maxArgs(n int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) > n {
			return usagef("%s: expected at most %d argument(s), got %d", cmd.Name(), n, len(args))
		}
		return nil
	}
}

func minArgs(n int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) < n {
			return usagef("%s: expected at least %d argument(s)", cmd.Name(), n)
		}
		return nil
	}
}

func newBackendsCommand(opts *Options) *cobra.Command {
	return &cobra.Command{
		Use:   "backends",
		Short: "List backends and whether each one works",
		Long: `List the registered schemes and whether each backend can be used: is its
CLI installed, is it signed in or unlocked. Exits 3 if any backend is
unavailable. Never prints secrets.`,
		Args: exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			schemes := opts.Registry.Schemes()
			statuses := make([]backend.Status, len(schemes))
			var wg sync.WaitGroup
			for i, s := range schemes {
				b, _ := opts.Registry.Lookup(s)
				wg.Add(1)
				go func() {
					defer wg.Done()
					ctx := cmd.Context()
					if opts.Timeout > 0 {
						var cancel context.CancelFunc
						ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
						defer cancel()
					}
					statuses[i] = b.Status(ctx)
				}()
			}
			wg.Wait()

			tw := tabwriter.NewWriter(opts.Stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "SCHEME\tSTATUS\tDETAIL")
			allOK := true
			for i, s := range schemes {
				state := "ok"
				if !statuses[i].Available {
					state = "unavailable"
					allOK = false
				}
				fmt.Fprintf(tw, "%s://\t%s\t%s\n", s, state, statuses[i].Detail)
			}
			if err := tw.Flush(); err != nil {
				return err
			}
			if !allOK {
				return &exitcode.ExitError{Code: exitcode.Unavailable}
			}
			return nil
		},
	}
}
