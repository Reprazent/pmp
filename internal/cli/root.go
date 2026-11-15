// Package cli wires pmp's commands and flags.
package cli

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Reprazent/pmp/internal/backend"
)

// BuildInfo is set at build time through -ldflags in cmd/pmp.
type BuildInfo struct {
	Version string
	Commit  string
	Date    string
}

// Options are the global flags plus the things tests want to swap out.
type Options struct {
	Timeout  time.Duration
	Quiet    bool
	Verbose  bool
	NoUnlock bool

	Registry *backend.Registry

	Stdout io.Writer
	Stderr io.Writer
	Getenv func(string) string
	// Environ returns the environment passed on to `run`'s child.
	Environ func() []string
	// Exec replaces the process (syscall.Exec). Tests swap it out.
	Exec func(argv0 string, argv []string, env []string) error
}

// Warnf prints a warning to stderr unless --quiet is set.
func (o *Options) Warnf(format string, args ...any) {
	if o.Quiet {
		return
	}
	fmt.Fprintf(o.Stderr, "pmp: warning: "+format+"\n", args...)
}

// Debugf prints to stderr when --verbose is set. Never pass secret values.
func (o *Options) Debugf(format string, args ...any) {
	if !o.Verbose {
		return
	}
	fmt.Fprintf(o.Stderr, "pmp: "+format+"\n", args...)
}

// NewRootCommand builds the pmp command tree.
func NewRootCommand(info BuildInfo, opts *Options) *cobra.Command {
	if opts.Stdout == nil {
		opts.Stdout = os.Stdout
	}
	if opts.Stderr == nil {
		opts.Stderr = os.Stderr
	}
	if opts.Getenv == nil {
		opts.Getenv = os.Getenv
	}
	if opts.Environ == nil {
		opts.Environ = os.Environ
	}
	if opts.Exec == nil {
		opts.Exec = execProcess
	}
	if opts.Registry == nil {
		opts.Registry = backend.NewRegistry()
	}

	root := &cobra.Command{
		Use:   "pmp",
		Short: "Read secrets from several password managers with one reference syntax",
		Long: `pmp, the poor-man's proxy, reads secrets from password managers.

References look like <scheme>://<path>, for example op://Private/GitHub/token
or bw://GitHub/password. pmp never writes or stores secrets.`,
		Version:       fmt.Sprintf("%s (commit %s, built %s)", info.Version, info.Commit, info.Date),
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRun: func(cmd *cobra.Command, _ []string) {
			cmd.SetContext(backend.WithSettings(cmd.Context(), backend.Settings{
				NoUnlock: opts.NoUnlock,
				Timeout:  opts.Timeout,
				Warnf:    opts.Warnf,
				Debugf:   opts.Debugf,
			}))
		},
	}
	root.SetOut(opts.Stdout)
	root.SetErr(opts.Stderr)
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return usageErr(err) })

	f := root.PersistentFlags()
	f.DurationVar(&opts.Timeout, "timeout", 30*time.Second, "timeout per backend call")
	f.BoolVarP(&opts.Quiet, "quiet", "q", false, "suppress warnings")
	f.BoolVarP(&opts.Verbose, "verbose", "v", false, "log references and timings to stderr (never secret values)")
	f.BoolVar(&opts.NoUnlock, "no-unlock", false, "never prompt to unlock a backend; fail instead")

	root.AddCommand(newGetCommand(opts), newEnvCommand(opts), newRunCommand(opts), newBackendsCommand(opts))
	return root
}

// Execute runs root and normalises cobra's own errors into usage errors.
func Execute(root *cobra.Command) error {
	err := root.Execute()
	if err != nil && isCobraUsageError(err) {
		return usageErr(err)
	}
	return err
}

func isCobraUsageError(err error) bool {
	msg := err.Error()
	for _, prefix := range []string{"unknown command", "unknown flag", "unknown shorthand flag", "accepts ", "requires at least", "invalid argument"} {
		if strings.HasPrefix(msg, prefix) {
			return true
		}
	}
	return false
}
