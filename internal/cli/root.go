// Package cli wires pmp's commands and flags.
package cli

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"
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

	Stdout io.Writer
	Stderr io.Writer
	Getenv func(string) string
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

	root := &cobra.Command{
		Use:   "pmp",
		Short: "Read secrets from several password managers with one reference syntax",
		Long: `pmp, the poor-man's proxy, reads secrets from password managers.

References look like <scheme>://<path>, for example op://Private/GitHub/token
or bw://GitHub/password. pmp never writes or stores secrets.`,
		Version:       fmt.Sprintf("%s (commit %s, built %s)", info.Version, info.Commit, info.Date),
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetOut(opts.Stdout)
	root.SetErr(opts.Stderr)
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return usageErr(err) })

	f := root.PersistentFlags()
	f.DurationVar(&opts.Timeout, "timeout", 30*time.Second, "timeout per backend call")
	f.BoolVarP(&opts.Quiet, "quiet", "q", false, "suppress warnings")
	f.BoolVarP(&opts.Verbose, "verbose", "v", false, "log references and timings to stderr (never secret values)")
	f.BoolVar(&opts.NoUnlock, "no-unlock", false, "never prompt to unlock a backend; fail instead")

	return root
}
