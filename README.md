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

### `pmp backends`

```console
$ pmp backends
SCHEME  STATUS       DETAIL
bw://   ok           locked as me@example.com; pmp will prompt to unlock
op://   ok           cli: signed in as me@example.com
```

Lists every backend and whether it can be used right now. Exits 3 if any
backend is unavailable, so `pmp backends >/dev/null && ...` works in scripts.
It never prints secrets. Use `-v` with other commands to see which
references go to which backend and how long each took.

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

### 1Password (`op://`)

Same syntax as 1Password's own
[secret references](https://developer.1password.com/docs/cli/secret-reference-syntax/),
passed through unchanged:

```
op://<vault>/<item>[/<section>]/<field>[?attribute=otp]
```

pmp picks the first of these that applies:

1. `OP_SERVICE_ACCOUNT_TOKEN` is set: the 1Password SDK with that service
   account. Good for CI; service accounts can't see Personal/Private vaults.
2. `PMP_OP_ACCOUNT` is set (account name or UUID, as in the app's sidebar):
   the SDK through the desktop app, which asks for approval or Touch ID.
   Turn on *Settings → Developer → Integrate with other apps*. If this fails
   and `op` is installed, pmp warns and falls back to the CLI.
3. `op` is on `PATH`: one `op inject` call for all references (with
   `--account $PMP_OP_ACCOUNT` if set).
4. Otherwise 1Password is unavailable (exit 3, or skipped with
   `--skip-unavailable`).

Desktop app integration needs a cgo build. Release binaries have it; a
`CGO_ENABLED=0` build falls back to the `op` CLI.

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

Tools are pinned in `mise.toml`. Backend tests use fake `op` and `bw`
executables, so they don't need accounts. To test against a real 1Password
account:

```sh
PMP_INTEGRATION=1 PMP_TEST_OP_REF=op://Private/Some/field go test ./internal/backend/onepassword -run Integration
```

```sh
mise run test    # unit tests with -race
mise run lint    # golangci-lint and go mod tidy check
mise run fuzz    # every fuzz target, FUZZTIME each (default 30s)
mise run build   # ./bin/pmp
```

### Releasing

Releases are automatic, using
[semantic-release](https://semantic-release.gitbook.io/) and
[Conventional Commits](https://www.conventionalcommits.org/). Nobody tags by
hand. After CI passes on `main`, the release workflow works out the next
version from the commits since the last tag:

| Commit | Release |
|--------|---------|
| `fix: ...`, `perf: ...` | patch |
| `feat: ...` | minor |
| `feat!: ...` or a `BREAKING CHANGE:` footer | major |
| `docs:`, `chore:`, `test:`, `ci:`, `build:`, `refactor:` | none |

It then tags `vX.Y.Z` and creates a draft GitHub release with generated
notes. Native cgo builds for darwin and linux (amd64, arm64) are attached
with checksums and build provenance, and the release is published. Finally it
commits the updated `Formula/pmp.rb` to `main`.

Pushing to a `beta` branch makes pre-releases (`vX.Y.Z-beta.N`), which skip
the Homebrew formula. Config lives in `.releaserc.json`. The `v0.0.0` tag on
the first commit is the baseline that makes the first release `v0.1.0`.

To preview the next version locally:

```sh
npx semantic-release@24 --dry-run --no-ci --plugins @semantic-release/commit-analyzer
```
