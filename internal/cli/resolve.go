package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/Reprazent/pmp/internal/backend"
	"github.com/Reprazent/pmp/internal/envfile"
	"github.com/Reprazent/pmp/internal/ref"
)

// resolver builds a Resolver. dir holds the env file's directives, if any.
func (o *Options) resolver(skipUnavailable bool, dir envfile.Directives) (*backend.Resolver, error) {
	ttl, err := o.cacheTTL(dir)
	if err != nil {
		return nil, err
	}
	r := &backend.Resolver{
		Registry:        o.Registry,
		Timeout:         o.Timeout,
		SkipUnavailable: skipUnavailable,
		Refresh:         o.Refresh,
		Warnf:           o.Warnf,
		Debugf:          o.Debugf,
	}
	if ttl > 0 {
		if o.Cache == nil {
			o.Warnf("caching isn't supported on this platform; ignoring the cache TTL")
		} else {
			r.Cache, r.CacheTTL = o.Cache, ttl
			o.Debugf("cache: ttl %s", ttl)
		}
	}
	return r, nil
}

// cacheTTL picks the TTL: --cache-ttl, then the env file's
// `# pmp: cache-ttl=`, then $PMP_CACHE_TTL. Zero means no caching.
func (o *Options) cacheTTL(dir envfile.Directives) (time.Duration, error) {
	switch {
	case o.cacheTTLSet:
		if o.CacheTTL < 0 {
			return 0, usagef("--cache-ttl can't be negative")
		}
		return o.CacheTTL, nil
	case dir.HasCacheTTL:
		return dir.CacheTTL, nil
	}
	v := o.Getenv("PMP_CACHE_TTL")
	if v == "" {
		return 0, nil
	}
	d, err := envfile.ParseTTL(v)
	if err != nil {
		return 0, usagef("PMP_CACHE_TTL: %v", err)
	}
	return d, nil
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
	entries, dir, err := envfile.ParseFile(path)
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

	res, err := o.resolver(skipUnavailable, dir)
	if err != nil {
		return nil, err
	}
	values, err := res.Resolve(ctx, refs)
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
