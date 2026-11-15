# pmp

pmp (the poor-man's proxy, or password manager proxy) reads secrets from
1Password and Bitwarden with one reference syntax. It only reads. It can't
write secrets, and it doesn't store or cache anything.

The design is in [docs/SPEC.md](docs/SPEC.md).

## Install

With Homebrew, from the tap in this repository:

```sh
brew tap reprazent/pmp https://github.com/Reprazent/pmp
brew install reprazent/pmp/pmp
```

With mise:

```sh
mise use -g github:Reprazent/pmp
```

From source:

```sh
go install github.com/Reprazent/pmp/cmd/pmp@latest
```

A build without cgo can't use the 1Password desktop app and falls back to the
`op` CLI (see [1Password](#1password-op)). The release binaries include cgo.

## Usage

### `pmp get`

```sh
pmp get <reference>
pmp get -n <reference>   # add a trailing newline
```

Prints one secret with no trailing newline, so you can use it in `$(...)` or
a pipe. Only the secret goes to stdout. Warnings and errors go to stderr.

### `pmp env`

```sh
eval "$(pmp env)"                     # sh/bash/zsh: export KEY='value'
pmp env ./deploy.env
pmp env --format=dotenv ./deploy.env  # KEY="value"
pmp env --format=json | jq .
pmp env --skip-unavailable
```

Reads an [env file](#env-files), resolves its references and prints the
variables. If any reference fails, pmp prints nothing and exits non-zero.

`--skip-unavailable` changes that for backends that aren't installed or
unlocked: pmp leaves their variables out and prints a warning. A secret that's
missing from a backend that works still fails.

### `pmp run`

```sh
pmp run -- terraform plan
pmp run --env-file ./deploy.env --skip-unavailable -- ./deploy.sh
```

Resolves the env file and then execs the command with those variables added
to its environment, overriding any inherited ones. The secrets never touch
stdout, and you get the command's exit code. pmp stops reading flags at the
command name, so the `--` is optional.

### `pmp backends`

```console
$ pmp backends
SCHEME  STATUS       DETAIL
bw://   ok           locked as me@example.com; pmp will prompt to unlock
op://   ok           cli: signed in as me@example.com
```

Shows whether each backend works right now. It exits 3 if one doesn't, so
`pmp backends >/dev/null && ...` works in scripts. To see which reference
went to which backend and how long it took, add `-v` to any other command.
Neither prints secret values.

## References

A reference looks like `<scheme>://<path>[?<query>]`. The path syntax depends
on the backend. Where a backend splits the path on `/`, write a literal slash
in a name as `%2F`.

### 1Password (`op://`)

The syntax is 1Password's own
[secret reference](https://developer.1password.com/docs/cli/secret-reference-syntax/)
format, and pmp passes it through untouched:

```
op://<vault>/<item>[/<section>]/<field>[?attribute=otp]
```

pmp uses the first of these that applies:

1. If `OP_SERVICE_ACCOUNT_TOKEN` is set, it uses the 1Password SDK with that
   service account. This suits CI, but service accounts can't see Personal or
   Private vaults.
2. If `PMP_OP_ACCOUNT` is set to an account name or UUID (as shown in the
   app's sidebar), it goes through the desktop app, which asks you to approve
   or use Touch ID. Turn on *Settings → Developer → Integrate with other
   apps* first. If this fails and `op` is installed, pmp warns and uses the
   CLI.
3. If `op` is on your `PATH`, it makes one `op inject` call for all
   references, passing `--account $PMP_OP_ACCOUNT` if that's set.
4. Otherwise 1Password counts as unavailable: exit 3, or skipped with
   `--skip-unavailable`.

### Bitwarden (`bw://`)

pmp reads your personal vault through the
[`bw` CLI](https://bitwarden.com/help/cli/).

```
bw://<item>/<field>[?folder=<name>|collection=<name>][&org=<name>]
bw://<item>/<field>?id=<uuid>
```

```sh
pmp get bw://GitHub/password
pmp get bw://AWS/access_key_id?folder=Work      # custom field, folder "Work"
pmp get "bw://AWS/password?folder=Work%2FInfra" # nested folder "Work/Infra"
pmp get "bw://Shared/password?collection=Infra&org=Acme"
pmp get bw://Anthropic/password?id=3f1c...      # by item id
```

The path is always `<item>/<field>`. `username`, `password`, `totp`, `notes`
and `uri` (the first URI) are built-in fields, and `totp` gives you the
current code. Any other field name is looked up in the item's custom fields.

If a name matches more than one item, pmp exits 4 and lists their ids. Add
`?id=` to pick one. With `?id=` the item name is just a label, and pmp warns
if it no longer matches the item.

pmp uses `BW_SESSION` when it's set. If it isn't and there's a terminal, pmp
runs `bw unlock --raw` and asks for your master password on the terminal.
That works inside `eval "$(pmp env)"` as well. pmp keeps the session in
memory for that run only, so you get one prompt per command. With no
terminal, or with `--no-unlock`, it exits 3 instead. If the prompts get
annoying:

```sh
export BW_SESSION="$(bw unlock --raw)"
```

## Env files

`pmp env` and `pmp run` read one env file, in a subset of the dotenv format:

```sh
# comments allowed
GITHUB_TOKEN="op://Personal/GitHub/token"
AWS_ACCESS_KEY_ID="bw://AWS/access_key_id?folder=Work"
EDITOR="nvim"                        # plain values pass through unchanged
export FOO='op://Vault/Item/field'   # optional `export` prefix
```

Each line is `KEY=VALUE`, and keys match `[A-Za-z_][A-Za-z0-9_]*`. Single
quotes are literal. Double quotes understand `\\`, `\"`, `\n` and `\$`. An
unquoted value ends at ` #`, which starts a comment. There's no variable
interpolation, and if a key appears twice the last value wins.

A value counts as a reference only if it starts with a scheme pmp knows,
followed by `://`. Everything else is passed through as is, including URLs
like `https://...`.

When you don't name a file, pmp uses `$PMP_ENV_FILE`, and if that's unset,
`~/.authenticated-env`. Every command follows the same order.

## Global flags

| Flag | Description |
|------|-------------|
| `--timeout <dur>` | Timeout per backend call (default `30s`). |
| `-q`, `--quiet` | Don't print warnings. |
| `-v`, `--verbose` | Log references and timings to stderr. Never logs secret values. |
| `--no-unlock` | Fail instead of prompting to unlock a backend. |
| `--version`, `--help` | |

## Exit codes

| Code | Meaning |
|------|---------|
| 0 | success |
| 1 | other error |
| 2 | usage error or invalid reference |
| 3 | backend unavailable (CLI missing, locked, not signed in) |
| 4 | secret not found, or ambiguous |
| 5 | timeout |

When several references fail for different reasons, the code follows this
priority: 2, 3, 5, 4, then 1.

## Development

`mise.toml` pins the tools.

```sh
mise run test    # unit tests with -race
mise run lint    # golangci-lint and a go mod tidy check
mise run fuzz    # every fuzz target, $FUZZTIME each (default 30s)
mise run build   # builds ./bin/pmp
```

The backend tests run against fake `op` and `bw` executables, so you don't
need accounts. To test against a real 1Password account:

```sh
PMP_INTEGRATION=1 PMP_TEST_OP_REF=op://Private/Some/field \
  go test ./internal/backend/onepassword -run Integration
```

### Releasing

[semantic-release](https://semantic-release.gitbook.io/) does the releases,
based on [Conventional Commits](https://www.conventionalcommits.org/). Don't
tag by hand. Once CI passes on `main`, the release workflow looks at the
commits since the last tag:

| Commit | Release |
|--------|---------|
| `fix:`, `perf:` | patch |
| `feat:` | minor |
| `feat!:` or a `BREAKING CHANGE:` footer | major |
| `docs:`, `chore:`, `test:`, `ci:`, `build:`, `refactor:` | none |

If there's something to release, the workflow tags `vX.Y.Z` and opens a
draft GitHub release. The build jobs then attach native cgo binaries for
darwin and linux (amd64 and arm64), along with checksums and build
provenance, and publish it. The last job commits the new `Formula/pmp.rb` to
`main`.

Pushes to the `beta` branch make pre-releases (`vX.Y.Z-beta.N`) and leave the
Homebrew formula alone. The config is in `.releaserc.json`. The `v0.0.0` tag on
the first commit is only there so the first release came out as `v0.1.0`.

To see what the next version would be:

```sh
npx semantic-release@24 --dry-run --no-ci --plugins @semantic-release/commit-analyzer
```
