package cli

import (
	"context"
	"errors"
	"fmt"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

func newCacheCommand(opts *Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cache",
		Short: "List or clear cached values",
		Long: `Cached values live in the macOS login Keychain, under the service "pmp".
Caching is off unless --cache-ttl, a "# pmp: cache-ttl=" line in the env file,
or $PMP_CACHE_TTL sets a TTL.`,
		Args: exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	cmd.AddCommand(newCacheListCommand(opts), newCacheClearCommand(opts))
	return cmd
}

// cacheCtx bounds a cache subcommand by --timeout.
func (o *Options) cacheCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	if o.Timeout > 0 {
		return context.WithTimeout(ctx, o.Timeout)
	}
	return context.WithCancel(ctx)
}

var errNoCache = errors.New("caching isn't supported on this platform")

func newCacheListCommand(opts *Options) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List cached references and when they expire (never values)",
		Args:  exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if opts.Cache == nil {
				return errNoCache
			}
			ctx, cancel := opts.cacheCtx(cmd.Context())
			defer cancel()
			entries, err := opts.Cache.List(ctx)
			if err != nil {
				return err
			}
			now := time.Now()
			tw := tabwriter.NewWriter(opts.Stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "REFERENCE\tEXPIRES\tSTATUS")
			for _, e := range entries {
				state := "valid"
				if e.Expired(now) {
					state = "expired"
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\n", e.Ref, e.Expires.Local().Format(time.RFC3339), state)
			}
			return tw.Flush()
		},
	}
}

func newCacheClearCommand(opts *Options) *cobra.Command {
	return &cobra.Command{
		Use:   "clear [reference...]",
		Short: "Delete every cached value, or only the given references",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if opts.Cache == nil {
				return errNoCache
			}
			ctx, cancel := opts.cacheCtx(cmd.Context())
			defer cancel()
			if len(args) == 0 {
				return opts.Cache.Clear(ctx)
			}
			for _, r := range args {
				ok, err := opts.Cache.Delete(ctx, r)
				if err != nil {
					return err
				}
				if !ok {
					opts.Debugf("cache: %s wasn't cached", r)
				}
			}
			return nil
		},
	}
}
