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

### `pmp get`

```sh
pmp get <reference>
pmp get -n <reference>   # with a trailing newline
```

Prints one secret to stdout without a trailing newline, so it's safe in
`$(...)` and pipes. Only the secret goes to stdout; warnings and errors go to
stderr.

### `pmp env`

```sh
eval "$(pmp env)"                         # sh/bash/zsh: export KEY='value'
pmp env ~/.authenticated-env
pmp env --format=dotenv ./deploy.env      # KEY="value"
pmp env --format=json | jq .
pmp env --skip-unavailable                # skip refs for missing/locked backends
```

Resolves every reference in an env file and prints the variables. Resolution
is all or nothing: if a reference fails, nothing is printed and pmp exits
non-zero. With `--skip-unavailable`, references whose backend isn't installed
or unlocked are left out with a warning; a secret missing from a working
backend still fails.

### `pmp run`

```sh
pmp run -- terraform plan
pmp run --env-file ./deploy.env --skip-unavailable -- ./deploy.sh
```

Resolves the env file and replaces itself with the command, with the
variables added to its environment (they override inherited ones). Secrets
never go to stdout. The exit code is the command's. Flags after the command
name belong to the command, so `--` is optional.

### Replacing `auth-env`

```sh
# ~/.authenticated-env holds references, not secrets
GITLAB_TOKEN="op://Private/GitLab/token"
ANTHROPIC_API_KEY="bw://Anthropic/password"
```

| Before              | With pmp                                         |
|---------------------|--------------------------------------------------|
| `. auth-env`        | `eval "$(pmp env --skip-unavailable)"`           |
| `auth-env cmd args` | `pmp run --skip-unavailable -- cmd args`         |

### References

A reference is `<scheme>://<path>[?<query>]`. Each backend defines its own
path syntax (see below). Path segments are percent-decoded where a backend
splits on `/`, so `%2F` is a literal slash in a name.

### Bitwarden (`bw://`)

Uses the [`bw` CLI](https://bitwarden.com/help/cli/) and your personal vault.

```
bw://<item>/<field>[?folder=<name>|collection=<name>][&org=<name>]
bw://<item>/<field>?id=<uuid>
```

```sh
pmp get bw://GitHub/password
pmp get bw://AWS/access_key_id?folder=Work      # custom field in folder "Work"
pmp get "bw://AWS/password?folder=Work%2FInfra" # nested folder "Work/Infra"
pmp get "bw://Shared/password?collection=Infra&org=Acme"
pmp get bw://Anthropic/password?id=3f1c...      # by item id
```

- The path is always `<item>/<field>`. Write a `/` inside a name as `%2F`.
- `username`, `password`, `totp`, `notes` and `uri` (the first URI) are
  reserved. Any other name is a custom field.
- `totp` returns the current code.
- If a name matches several items, pmp exits 4 and lists their ids so you
  can add `?id=`. With `?id=`, the item name is only a label; pmp warns if it
  no longer matches.

**Unlocking.** pmp uses `BW_SESSION` if it's set. Otherwise, if there's a
terminal, it runs `bw unlock --raw` and asks for your master password on the
terminal (this works inside `eval "$(pmp env)"` too). The session is kept in
memory for that one run, so you get one prompt per `pmp` call. Without a
terminal, or with `--no-unlock`, pmp exits 3. To avoid repeated prompts:

```sh
export BW_SESSION="$(bw unlock --raw)"
```

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
