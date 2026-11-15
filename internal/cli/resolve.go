package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"

	"github.com/Reprazent/pmp/internal/backend"
	"github.com/Reprazent/pmp/internal/envfile"
	"github.com/Reprazent/pmp/internal/ref"
)

func (o *Options) resolver(skipUnavailable bool) *backend.Resolver {
	return &backend.Resolver{
		Registry:        o.Registry,
		Timeout:         o.Timeout,
		SkipUnavailable: skipUnavailable,
		Warnf:           o.Warnf,
		Debugf:          o.Debugf,
	}
}

// resolveEnvFile loads the env file (after discovery) and returns its
// entries with references replaced by secrets. Skipped references are left
// out. On any error it returns no entries.
func (o *Options) resolveEnvFile(ctx context.Context, explicit string, skipUnavailable bool) ([]envfile.Entry, error) {
	path, err := envfile.Discover(explicit, o.Getenv)
	if err != nil {
		return nil, err
	}
	o.Debugf("env file: %s", path)
	entries, err := envfile.ParseFile(path)
	if err != nil {
		if errors.Is(err, envfile.ErrSyntax) {
			return nil, usageErr(err)
		}
		if errors.Is(err, fs.ErrNotExist) {
			return nil, usagef("env file %s doesn't exist", path)
		}
		return nil, err
	}

	schemes := o.Registry.Schemes()
	var refs []ref.Ref
	for _, e := range entries {
		if !ref.IsReference(e.Value, schemes) {
			continue
		}
		r, err := ref.Parse(e.Value)
		if err != nil {
			return nil, usageErr(fmt.Errorf("%s:%d: %s: %w", path, e.Line, e.Key, err))
		}
		refs = append(refs, r)
	}

	values, err := o.resolver(skipUnavailable).Resolve(ctx, refs)
	if err != nil {
		return nil, err
	}

	out := make([]envfile.Entry, 0, len(entries))
	for _, e := range entries {
		if ref.IsReference(e.Value, schemes) {
			v, ok := values[e.Value]
			if !ok {
				continue // skipped with --skip-unavailable
			}
			e.Value = v
		}
		out = append(out, e)
	}
	return out, nil
}
