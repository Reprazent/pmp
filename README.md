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
