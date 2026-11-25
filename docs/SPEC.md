# Spec: pmp, the poor-man's proxy (or password manager proxy)

A read-only CLI that fetches secrets from several password managers.

> Draft.

## 1. Goal

I use `op read`, `op run` and `op inject` in my dotfiles. pmp replaces those
reads, but with one reference syntax that works across password managers.

### Main use case: `auth-env`

`local/bin/auth-env` in `codeberg.org/reprazent/dotfiles` reads every
env-var-shaped field from the 1Password item `authenticated-env` and passes
them to `op inject` or `op run`.

With pmp, each machine gets its own `~/.authenticated-env`. The file holds
references, not secrets, and can point at more than one password manager:

```sh
GITLAB_TOKEN="op://Private/GitLab/token"
ANTHROPIC_API_KEY="bw://API keys/Anthropic/password"
```

| Today                              | With pmp                                     |
|------------------------------------|----------------------------------------------|
| `. auth-env`                       | `eval "$(pmp env ~/.authenticated-env)"`     |
| `auth-env cmd args`                | `pmp run --env-file ~/.authenticated-env -- cmd args` |
| no `op` (work mac): silent no-op   | `auth-env` passes `--skip-unavailable`, so refs for missing or locked backends are skipped with a warning |
| `op whoami \|\| op signin`         | the backend handles auth (desktop app prompt, `BW_SESSION`) |

`auth-env` then becomes a thin wrapper around those two calls. When
`--env-file` isn't given, pmp uses `$PMP_ENV_FILE`, then `~/.authenticated-env`.

### Out of scope

- Writing, creating, editing or deleting secrets.
- Storing anything. pmp is not a password manager and doesn't cache secrets on disk.
- Its own login flows. Each backend's CLI or SDK session handles that.

## 2. Backends

| Phase | Scheme       | Backend                 | Implementation            |
|-------|--------------|-------------------------|---------------------------|
| v0.1  | `op://`      | 1Password               | official Go SDK (`onepassword-sdk-go`) via desktop app or service account, falling back to the `op` CLI |
| v0.1  | `bw://`      | Bitwarden Password Manager (personal vault) | `bw` CLI, because there's no public SDK for vault items (see 6.1) |
| later | `keychain://`| macOS Keychain          | `security` CLI or native API |
| later | `secret-service://` | Linux (libsecret, GNOME Keyring, KWallet) | `secret-tool` or D-Bus |
| maybe | `bws://`     | Bitwarden Secrets Manager | official Go SDK (`bitwarden/sdk-go`), not in the first iteration |

Each backend registers itself by scheme. Adding one should only touch its own
package plus a single registration line.

## 3. Secret references

```
<scheme>://<path>[?<query>]
```

### 3.1 1Password (`op://`)

Same syntax as 1Password itself, so existing dotfiles don't need edits. pmp
hands the reference unchanged to the SDK's `Secrets().ResolveAll`, or to
`op read`/`op inject` in the CLI fallback.

```
op://<vault>/<item>[/<section>]/<field>
op://Personal/GitHub/token
op://Work/AWS/credentials/access_key_id?attribute=otp
```

### 3.2 Bitwarden (`bw://`)

```
bw://<item>/<field>[?folder=<name>|collection=<name>][&org=<name>]
bw://<item>/<field>?id=<uuid>

bw://GitHub/password
bw://AWS/access_key_id?folder=Work # custom field, scoped to a folder
bw://Anthropic/password?id=<uuid>  # lookup by item id
```

Bitwarden doesn't reserve any characters in names (folders even nest with
`/`), so the path is always exactly `<item>/<field>` and the optional parts
are query parameters. Segments and parameter values are percent-decoded, so
`%2F` is a literal `/`.

- `folder=` matches the folder's full name (`folder=Work/AWS` for nested
  folders). `collection=` matches a collection name, and `org=` narrows
  items and collections to one organization.
- With `id=`, the item segment is only a readable label. If it no longer
  matches the item's name, pmp warns but still returns the value.

`username`, `password`, `totp`, `notes` and `uri` (the first URI) are
reserved field names, as are `private_key`, `public_key` and `fingerprint`
for SSH key items (`sshKey.privateKey`, `.publicKey`, `.keyFingerprint`). Any other field name is looked up in the item's custom
fields; linked fields follow their link.

If a name matches more than one item, pmp fails with exit code 4 and lists the
candidate ids rather than picking one.

### 3.3 Keychains (later, rough sketch)

```
keychain://<service>[/<account>]
secret-service://<attr>=<value>[&<attr>=<value>...]
```

I'll settle the exact form when implementing them.

## 4. Commands

### 4.1 `get`

```
pmp get <reference>
```

Prints the secret to stdout without a trailing newline, so it's safe in
`$(...)` and pipes. `-n`/`--newline` adds one. Only the secret goes to stdout;
everything else goes to stderr.

### 4.2 `env` and `run`

Both read one env file. The format is a subset of dotenv:

```sh
# comments allowed
GITHUB_TOKEN="op://Personal/GitHub/token"
AWS_ACCESS_KEY_ID="bw://Work/AWS/access_key_id"
EDITOR="nvim"                        # plain values pass through unchanged
export FOO='op://Vault/Item/field'   # optional `export` prefix
```

- Lines are `KEY=VALUE`, with optional single or double quotes around the
  value. No variable interpolation in v0.1.
- A value counts as a reference only if it starts with a registered scheme
  (`^<scheme>://`). Anything else, unknown schemes included, is a literal.

```
pmp env <file>                 # `export KEY='value'` lines, POSIX sh quoting
pmp env --format=fish <file>   # `set -gx KEY 'value'`
pmp env --format=dotenv <file> # KEY="value"
pmp env --format=json <file>
pmp run [--env-file <file>] -- <cmd> [args...]
```

`env` is meant for `eval "$(pmp env ~/.authenticated-env)"` in a shell rc.
`run` execs the command with the resolved variables added to its environment,
so secrets never hit stdout. It's the equivalent of `op run --env-file`.

Resolution is all or nothing. If any reference fails, pmp prints nothing and
exits non-zero. The only exception is `--skip-unavailable` (see 4.4).

### 4.3 `backends`

```
pmp backends
```

Lists the registered schemes and whether each one works: is the CLI there, is
it signed in or unlocked. Handy when debugging dotfiles. It never prints
secrets.

### 4.4 Global flags

- `--timeout <dur>`: per backend call, default 30s.
- `-q`/`--quiet`: suppress warnings.
- `--skip-unavailable` (`env` and `run` only): skip references whose backend
  isn't installed or unlocked, with a warning, instead of failing. Off by
  default. It lets one file work on a machine without 1Password, for example.
  A secret that's missing from a working backend still fails.
- `--no-unlock`: never prompt to unlock a backend; fail instead (see 5.1).
- `--version`, `--help`.

## 5. Behaviour

### 5.1 Authentication

1Password auth is picked in this order:

1. If `OP_SERVICE_ACCOUNT_TOKEN` is set, use the SDK with that service
   account. Good for headless use and CI, but service accounts can't see
   Personal/Private vaults.
2. If `PMP_OP_ACCOUNT` is set (account name or UUID, as in the app's sidebar),
   use the SDK through the desktop app. The app asks for approval or Touch ID.
   This needs *Settings → Developer → Integrate with other apps* turned on.
3. If `op` is on `PATH`, fall back to `op inject`, which is what `auth-env`
   does today.
4. Otherwise the backend is unavailable: exit 3, or skipped with
   `--skip-unavailable`.

Bitwarden uses the `bw` CLI with `BW_SESSION`. When `BW_SESSION` isn't set:

- If there's a terminal, pmp runs `bw unlock --raw` itself. The master
  password prompt goes to `/dev/tty`, never stdout, so it still works inside
  `eval "$(pmp env ...)"`. pmp uses that session for the current run only and
  then forgets it. All Bitwarden refs in one `env`/`run` call share it, so you
  get one prompt per call.
- Without a terminal (cron, CI, editor launchers), or with `--no-unlock`, pmp
  fails with exit 3 and suggests `export BW_SESSION=$(bw unlock --raw)`.

Later: an opt-in `--export-session` on `env` that also prints
`export BW_SESSION=...`, so later calls in the same shell skip the prompt.

pmp never stores credentials or session tokens.

### 5.2 Performance

Both `op` and `bw` are slow. For v0.1 I'm only doing the basics and will look
at speed properly later.

- `env` and `run` group references by backend and query the backends in
  parallel.
- Each backend gets one batched call where it can. For 1Password that's a
  single `Secrets().ResolveAll` (results and errors per reference), or a
  single `op inject` in the fallback. For Bitwarden it's one `bw list items`,
  or one `bw get item` per unique item, and pmp picks the fields out itself.
- Nothing is cached on disk.

Ideas for later: making the 1Password SDK the default path, keeping a
`bw serve` (local REST API) running to skip Node's startup, or a short-lived
in-memory agent.

### 5.3 Security

- Secrets only go to stdout (`get`, `env`) or to the child's environment (`run`).
- pmp never logs secret values, even with `--verbose`. Logs show references only.
- Backend CLIs run with a minimal environment.
- Shell output uses single quotes, with `'` escaped as `'\''`.

### 5.4 Exit codes

| Code | Meaning |
|------|---------|
| 0 | success |
| 1 | generic error |
| 2 | usage error or invalid reference |
| 3 | backend unavailable (CLI missing, locked, not signed in) |
| 4 | secret not found, or ambiguous |
| 5 | timeout |

## 6. Architecture

Go. 1Password has official SDKs for Go, Python and JS. Bitwarden's SDK
(Secrets Manager only) wraps a Rust core and has bindings for Go, Python, JS,
C#, Java, Ruby, PHP and C++. Go is in both lists, builds a single binary, and
has keychain libraries (`go-keychain`, D-Bus) for later.

### 6.1 SDK notes (checked 2026-11-14)

1Password SDK (`github.com/1password/onepassword-sdk-go`, MIT, actively
maintained):

- Out of beta since v0.1.0 (August 2024). Desktop app integration arrived in
  v0.4.0 and left beta in early 2026.
- Reads `op://` references with `client.Secrets().Resolve(ctx, ref)` and
  `ResolveAll(ctx, refs)`. `ResolveAll` returns an `IndividualResponses` map
  with a separate error per reference.
- Setup is `onepassword.NewClient(ctx, WithDesktopAppIntegration(account))`
  or `WithServiceAccountToken(token)`, plus
  `WithIntegrationInfo("pmp", version)`.
- With a service account, the core runs as WASM (extism/wazero), so it's pure Go.
- Desktop app integration needs cgo. It `dlopen`s
  `libop_sdk_ipc_client.dylib`/`.so` from the installed app
  (`/Applications/1Password.app/...`, `/opt/1Password/...`). A
  `CGO_ENABLED=0` build returns an error instead, so release builds need cgo
  for every target (see 8).
- Since service accounts can't read Personal/Private vaults, the desktop app
  is the path that matters for dotfiles.

Bitwarden SDK (`github.com/bitwarden/sdk-go`) only covers Secrets Manager
(access tokens, projects). It can't read Password Manager vault items, and it
needs cgo for the Rust library. The SDK that can read vault items
(`bitwarden/sdk-internal`) isn't a public API. So `bw://` uses the `bw` CLI,
and `bws://` waits.

### 6.2 Layout

```
cmd/pmp/                          main, flag wiring
internal/ref/                     reference parsing (scheme, path, query)
internal/envfile/                 env file parser and output formats
internal/backend/                 Backend interface and registry
internal/backend/onepassword/
internal/backend/bitwarden/
internal/backend/keychain/        (later, build tag darwin)
internal/backend/secretservice/   (later, build tag linux)
```

```go
type Backend interface {
    Scheme() string                                   // "op", "bw", ...
    Status(ctx context.Context) Status                // for `backends`
    Resolve(ctx context.Context, refs []Ref) (map[Ref]string, error) // batched
}
```

`get` is `Resolve` with a single ref. Errors are typed (`ErrUnavailable`,
`ErrNotFound`, `ErrAmbiguous`) and map onto the exit codes.

## 7. Testing

- Unit tests, including fuzzing, for reference parsing, env file parsing and
  shell quoting.
- Backend tests run against fake `op` and `bw` executables on `PATH` that
  return fixture JSON, so CI doesn't need real accounts.
- Integration tests against real vaults, opt-in through an env var.

## 8. Distribution

- GitHub Releases via GoReleaser for darwin and linux, amd64 and arm64. Builds
  use `CGO_ENABLED=1` on native runners (macOS for darwin, Linux for linux)
  because 1Password's desktop integration rules out a plain cross-compile. A
  `CGO_ENABLED=0` build still works but drops desktop app auth and falls back
  to the `op` CLI.
- A Homebrew tap (`Reprazent/homebrew-tap`). mise's `ubi`/`github` backends
  can install straight from the releases.

## 9. Milestones

1. v0.1: `get`, `env`, `run`; `op://` and `bw://`; exit codes; tests with fake CLIs.
2. v0.2: `backends` status command; fish and json output.
3. v0.3: `keychain://` (macOS).
4. v0.4: `secret-service://` (Linux).
5. Later: speed (see 5.2), templating like `op inject` for config files, and
   more backends (pass, gopass, Vault, AWS Secrets Manager).

## 10. Decisions

- Name: pmp, for poor-man's proxy or password manager proxy. A nod to [pmv](https://gitlab.com/gitlab-com/gl-infra/pmv).
- Go.
- One env file, no more.
- No templating and no `bws://` in v0.1.
- pmp prompts to unlock Bitwarden when it has a terminal and no `BW_SESSION`;
  the session lives for one run. `--export-session` comes later.
- `--skip-unavailable` is opt-in. By default a missing backend or secret is an
  error, and callers like `auth-env` pass the flag when they want the lenient
  behaviour.
- Speed waits until after v0.1.
