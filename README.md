# pmp

pmp, the poor-man's proxy (or password manager proxy), is a read-only CLI that
fetches secrets from several password managers using one reference syntax.

> Work in progress. See [docs/SPEC.md](docs/SPEC.md) for the design and
> [docs/PLAN.md](docs/PLAN.md) for the implementation plan.

## Install

### Homebrew

The tap lives in this repository:

```sh
brew tap reprazent/pmp https://github.com/Reprazent/pmp
brew install reprazent/pmp/pmp
```

### mise

```sh
mise use -g github:Reprazent/pmp
```

### From source

```sh
go install github.com/Reprazent/pmp/cmd/pmp@latest
```

## Usage

```
pmp [command] [flags]
```

### References

A reference is `<scheme>://<path>[?<query>]`. Each backend defines its own
path syntax (see below). Path segments are percent-decoded where a backend
splits on `/`, so `%2F` is a literal slash in a name.

### Env files

`pmp env` and `pmp run` read one env file, a subset of dotenv:

```sh
# comments allowed
GITHUB_TOKEN="op://Personal/GitHub/token"
AWS_ACCESS_KEY_ID="bw://AWS/access_key_id?folder=Work"
EDITOR="nvim"                        # plain values pass through unchanged
export FOO='op://Vault/Item/field'   # optional `export` prefix
```

- One `KEY=VALUE` per line. Keys match `[A-Za-z_][A-Za-z0-9_]*`.
- Single quotes are literal. Double quotes understand `\\`, `\"`, `\n` and
  `\$`. Unquoted values end at ` #` (a comment).
- No variable interpolation.
- If a key repeats, the last value wins.
- A value is a reference only if it starts with a registered scheme followed
  by `://`. Anything else, including unknown schemes like `https://`, is
  passed through as is.

The file is picked in this order, for every command:

1. the file given on the command line,
2. `$PMP_ENV_FILE`,
3. `~/.authenticated-env`.

### Global flags

| Flag | Description |
|------|-------------|
| `--timeout <dur>` | Timeout per backend call (default `30s`). |
| `-q`, `--quiet` | Suppress warnings. |
| `-v`, `--verbose` | Log references and timings to stderr. Secret values are never logged. |
| `--no-unlock` | Never prompt to unlock a backend; fail instead. |
| `--version`, `--help` | |

### Exit codes

| Code | Meaning |
|------|---------|
| 0 | success |
| 1 | generic error |
| 2 | usage error or invalid reference |
| 3 | backend unavailable (CLI missing, locked, not signed in) |
| 4 | secret not found, or ambiguous |
| 5 | timeout |

## Development

Tools are pinned in `mise.toml`.

```sh
mise run test    # unit tests with -race
mise run lint    # golangci-lint and go mod tidy check
mise run fuzz    # every fuzz target, FUZZTIME each (default 30s)
mise run build   # ./bin/pmp
```

### Releasing

Push a `v*` tag. The release workflow builds native cgo binaries for
darwin and linux (amd64, arm64), publishes a GitHub release with checksums and
build provenance, and commits the updated `Formula/pmp.rb` to `main`. Tags
with a `-` (like `v0.1.0-rc.1`) become pre-releases and leave the formula
alone.
