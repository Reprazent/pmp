# Implementation plan for pmp

Based on `docs/SPEC.md`. The repo has only the spec so far. Go 1.27 and `bw`
are available locally; `op` isn't, so the `op` fallback gets built and tested
against a fake `op`.

Each step ends with green `go test ./...` and its own commit. Every phase
also updates `README.md` with usage for what it added (commands, flags,
reference syntax, install), so the README always matches the code.

## Decisions taken while planning

- Exit code priority when several refs fail differently: 2 > 3 > 5 > 4 > 1.
- `Resolve` returns results keyed by the raw reference string.
- `bw://` segments are percent-decoded, and the path has a fixed shape (see
  Phase 3).
- Env file discovery is the same for `env` and `run`: explicit argument/flag,
  then `$PMP_ENV_FILE`, then `~/.authenticated-env`.
- Output formats in v0.1: `sh` (default), `dotenv`, `json`. Fish goes on the
  roadmap and waits for a contributor.
- `--skip-unavailable` treats a signed-out `op` as unavailable.
- `pmp backends` exits 3 if any backend is unavailable.
- `-v/--verbose` is a global flag.

## Phase 0: Scaffolding

1. `go mod init github.com/Reprazent/pmp`. `mise.toml` pins `go`,
   `golangci-lint`, `goreleaser`, and defines tasks (`test`, `lint`,
   `build`, `fuzz`, `snapshot`) so CI and local runs use the same commands.
2. `cmd/pmp/main.go` using **cobra** (subcommands, `--` handling for `run`,
   `--version`). Global flags: `--timeout` (30s), `-q/--quiet`,
   `-v/--verbose`, `--no-unlock`. Version info is injected via `-ldflags`
   (`main.version`, `main.commit`, `main.date`) by GoReleaser.
3. `internal/exitcode`: map typed errors to exit codes 0–5 in one place.
   `main` is the only place that calls `os.Exit`.
4. `.golangci.yml` with a modest linter set (`govet`, `staticcheck`,
   `errcheck`, `gosec`, `revive`).
5. CI and release workflows (see "GitHub Actions" below). They're set up in
   this phase so every later step runs through CI. The release workflow gets
   tested with a pre-release tag at the end of Phase 2.

## Phase 1: Core packages (no backends yet)

### `internal/ref`
- `type Ref struct{ Raw, Scheme, Path, RawQuery string }`, which stays
  comparable. `Query()` parses on demand.
- `Parse(s string) (Ref, error)`: needs `<scheme>://` and a non-empty path,
  and keeps the raw string so `op://` refs go to 1Password byte for byte.
- `IsReference(value string, schemes []string) bool` for env-file values.
- Tests: table tests plus `FuzzParse`.

### `internal/backend`
- `Backend` interface from spec 6.2, but `Resolve` returns
  `(map[string]string, error)` keyed by `Ref.Raw`. Also
  `Status{Available bool; Detail string}`.
- Errors: `ErrUnavailable`, `ErrNotFound`, `ErrAmbiguous` (with candidate
  ids), `ErrTimeout`, wrapped so `errors.Is` works. A per-ref `RefError{Ref,
  Err}`, and `MultiError` collecting them. The exit code comes from the
  highest-priority error (2 > 3 > 5 > 4 > 1).
- Registry: `Register(Backend)`, `Lookup(scheme)`, `Schemes()`. Backends are
  registered in `cmd/pmp/backends.go` (one line each), not via `init()`, so
  tests can build their own registry.
- `Resolver`: groups refs by scheme, runs backends concurrently (`errgroup`)
  with a per-backend `context.WithTimeout`, merges results. With
  `SkipUnavailable`, it drops refs whose backend returned `ErrUnavailable` and
  warns on stderr (unless `--quiet`). Any other error fails everything.

### `internal/envfile`
- Parser: comments, blank lines, optional `export `, `KEY=VALUE`, single or
  double quotes, trailing `# comment` after a value. Inside double quotes,
  only `\"` and `\\` escapes; no interpolation. Malformed lines give exit 2
  with the line number. Keys must match `^[A-Za-z_][A-Za-z0-9_]*$`. File
  order is kept.
- `Discover(explicit string) (string, error)`: argument/flag →
  `$PMP_ENV_FILE` → `~/.authenticated-env`. Used by both `env` and `run`.
- Formatters: `sh` (`export KEY='v'`, `'` → `'\''`), `dotenv`, `json`
  (object in file order).
- Tests: golden files, `FuzzParse`, `FuzzShQuote`, and a round-trip test
  that `eval`s the `sh` output in `sh` and compares the values.

## Phase 2: Commands with a fake backend

Built against an in-memory `fake` backend registered only in tests.

- `get <ref>`: writes the value with no trailing newline (`-n` adds one).
  An unknown scheme exits 2.
- `env [--format sh|dotenv|json] [--skip-unavailable] [<file>]`: buffers
  output and writes only when everything resolved.
- `run [--env-file <file>] [--skip-unavailable] -- cmd args`: resolves, then
  `syscall.Exec` with `os.Environ()` plus the resolved vars (resolved values
  win), so the child's exit code is pmp's.
- Tests: run the cobra root with `SetArgs`/`SetOut`/`SetErr`. For `run`, use
  the helper-process pattern from `TestMain`.

## Phase 3: Bitwarden backend (`internal/backend/bitwarden`)

### Reference format

Bitwarden doesn't reserve any characters in item, folder, collection or field
names. Names are free text that the client encrypts. Folders even use `/`
for nesting. So there's no character we could claim for markers like `{id}`.
Instead, the path has a fixed shape and everything optional moves into the
query string:

```
bw://<item>/<field>[?folder=<name>|collection=<name>][&org=<name>]
bw://<item>/<field>?id=<uuid>
```

- The path is always exactly two segments, `<item>/<field>`, each
  percent-decoded (`%2F` for a `/` in a name). More or fewer segments is a
  usage error (exit 2).
- `folder=` and `collection=` scope the item lookup (`folder=Work/AWS`
  works for nested folders). `org=` narrows collections when names repeat
  across organisations.
- `id=<uuid>` looks the item up by id. The `<item>` segment stays as a
  readable label. If it doesn't match the item's name, pmp warns (unless
  `--quiet`) but still returns the value, so renaming the item doesn't break
  anything.
- Reserved fields: `username`, `password`, `totp`, `notes`, `uri` (first
  URI). Anything else matches `fields[].name` exactly. If several custom
  fields share that name, the result is `ErrAmbiguous`.

Examples:

```
bw://GitHub/password
bw://AWS/access_key_id?folder=Work
bw://Anthropic/password?id=3f1c…
```

This is a change to spec 3.2, which I'll update when this phase lands.

### Implementation

- `internal/execx` (shared by both CLI backends): runs a command with a
  minimal environment (`PATH`, `HOME`, `TMPDIR`, `LANG`, `XDG_*` plus an
  allow-list per backend: `BW_SESSION`, `BITWARDENCLI_APPDATA_DIR`, `OP_*`).
  It honours the context, captures stderr for error classification, and
  never logs stdout.
- Session handling:
  - `bw status`: `unauthenticated` or a missing binary → `ErrUnavailable`.
  - Locked and no `BW_SESSION`: if `/dev/tty` opens and `--no-unlock` isn't
    set, run `bw unlock --raw` with stdin/stderr on `/dev/tty`, capture
    stdout, and keep the session in memory only. Otherwise `ErrUnavailable`
    with the `export BW_SESSION=$(bw unlock --raw)` hint.
  - A `sync.Once` means one prompt per run.
- Batching: `bw list items` once, plus `bw list folders` / `bw list
  collections` only when a ref uses `folder=`/`collection=`. Fields are
  picked out locally. `totp` is generated locally from `login.totp` so it
  doesn't need another CLI call.
- Matching: exact item name within the scope. No match → `ErrNotFound`.
  Several matches → `ErrAmbiguous` listing the ids and suggesting `?id=`.
- Tests: a fake `bw` (a small Go program built once in `TestMain` into a temp
  dir that's put on `PATH`). It serves fixture JSON and records invocations
  and environment, so tests can check batching and the minimal env.

## Phase 4: 1Password backend (`internal/backend/onepassword`)

The strategy is picked lazily, in spec 5.1 order:

1. `OP_SERVICE_ACCOUNT_TOKEN` → SDK with `WithServiceAccountToken`.
2. `PMP_OP_ACCOUNT` → SDK with `WithDesktopAppIntegration`. Built with a
   `cgo` tag; the `!cgo` stub reports unavailable and falls through to (3).
3. `op` on `PATH` → a single `op inject` call. The template wraps each ref
   in unique random markers, and the output is split on those markers (values
   can contain newlines). `op` stderr is classified into
   unavailable/not-found. If the batch fails, each ref is retried with
   `op read` so the error names the ref that broke. A signed-out `op`
   counts as unavailable.
4. Otherwise `ErrUnavailable`.

- SDK path: `Secrets().ResolveAll`, mapping each per-ref error. Passes
  `WithIntegrationInfo("pmp", version)`. The SDK sits behind a small
  interface so tests can fake it.
- `Status`: names the strategy and checks it works (`op whoami`, or creating
  the SDK client).
- Tests: a fake `op` like the fake `bw`. Real-vault tests only run when
  `PMP_INTEGRATION=1`.
- Check first: the current `onepassword-sdk-go` API, and how much the WASM
  core adds to binary size and startup time.

## Phase 5: `backends` and polish

- `pmp backends`: table of scheme, status and detail (`op: cli, signed in
  as …`, `bw: locked`). Exits 3 if any backend is unavailable, so
  `pmp backends && …` works in scripts.
- `--verbose`: refs, strategies and timings on stderr, never values.
- README: install, reference syntax, `auth-env` migration.

## GitHub Actions

### `.github/workflows/ci.yml`: runs on push to `main` and on pull requests

```yaml
jobs:
  lint:      # ubuntu-latest
    - actions/checkout
    - jdx/mise-action          # installs go, golangci-lint from mise.toml
    - mise run lint            # golangci-lint run; go mod tidy -diff
  test:
    strategy.matrix.os: [ubuntu-latest, macos-latest]
    - actions/checkout
    - jdx/mise-action
    - mise run test            # go test -race -count=1 ./...
    - mise run fuzz-short      # each Fuzz* target for 30s
  build:
    strategy.matrix: CGO_ENABLED 0 and 1 on both OSes
    - go build ./cmd/pmp       # catches broken cgo/!cgo build tags
  release-check:             # only on PRs touching .goreleaser.yaml or workflows
    - goreleaser check
    - goreleaser release --snapshot --clean (single target)
```

- The Go module cache is cached by `mise-action` / `actions/setup-go`.
- Real-vault integration tests don't run in CI (no secrets in CI). They're
  run by hand with `PMP_INTEGRATION=1 mise run test`.
- A nightly scheduled job runs the fuzzers for longer (5 min each) and
  uploads any new corpus entries as artifacts.

### `.github/workflows/release.yml`: runs on tags `v*`

cgo needs native builds, so the release uses GoReleaser's split/merge mode
(`goreleaser release --split`, then `goreleaser continue --merge`). That
needs GoReleaser Pro. **If we stay on the free version**, the fallback is the
same pipeline written out by hand: native build jobs upload archives as
artifacts, and a final job creates the release with `gh release create` and
updates the tap. I'll take the free route unless you already have Pro.

```yaml
jobs:
  build:
    strategy.matrix:
      - { os: macos-latest,      goos: darwin, goarch: arm64 }
      - { os: macos-13,          goos: darwin, goarch: amd64 }  # or cross-arch on arm64 with clang -arch x86_64
      - { os: ubuntu-latest,     goos: linux,  goarch: amd64 }
      - { os: ubuntu-24.04-arm,  goos: linux,  goarch: arm64 }
    steps:
      - checkout (fetch-depth: 0), mise-action
      - mise run test                      # don't release untested binaries
      - CGO_ENABLED=1 go build -trimpath -ldflags "-s -w -X main.version=${tag} ..."
      - tar.gz as pmp_${version}_${goos}_${goarch}.tar.gz (binary, LICENSE, README)
      - actions/upload-artifact
  release:
    needs: build
    permissions: { contents: write, id-token: write, attestations: write }
    steps:
      - download all artifacts
      - sha256sum > checksums.txt
      - actions/attest-build-provenance on the archives
      - gh release create $tag --generate-notes archives... checksums.txt
  homebrew:
    needs: release
    permissions: { contents: write }
    steps:
      - checkout main
      - render Formula/pmp.rb (url + sha256 per os/arch) from
        packaging/pmp.rb.tmpl
      - commit "brew: pmp ${version}" and push to main with GITHUB_TOKEN
```

The Homebrew tap lives in this repo, in `Formula/pmp.rb`, rather than in a
separate `homebrew-tap` repo. Because the repo isn't called `homebrew-*`,
users pass the URL once when they tap it:

```sh
brew tap reprazent/pmp https://github.com/Reprazent/pmp
brew install reprazent/pmp/pmp
```

- No PAT is needed; the workflow's own `GITHUB_TOKEN` can push. Pushes made
  with `GITHUB_TOKEN` don't trigger other workflows, so the formula commit
  doesn't set off CI again.
- If `main` gets branch protection later, either allow the Actions bot to
  bypass it or have the job open a PR instead of pushing.
- The formula commit lands on `main` just after the tag, so the tagged
  commit always has the previous version's formula. That's fine because
  `brew` reads the formula from the default branch, not from tags.
- `brew tap` clones the whole repo. That's fine for a small Go project.
- `brew audit --strict --online Formula/pmp.rb` runs in CI when the
  template changes.

- Asset names `pmp_<version>_<os>_<arch>.tar.gz` match what mise's
  `github:`/`ubi:` backends expect, so `mise use github:Reprazent/pmp`
  works without extra config.
- The Linux binaries link against glibc. Building on `ubuntu-22.04`
  instead of `latest` gives an older glibc baseline so they run on more
  distros.
- macOS binaries aren't signed or notarised in v0.1. Homebrew installs are
  fine; a direct download needs `xattr -d com.apple.quarantine`. Signing goes
  on the roadmap.
- To cut a release: `git tag v0.1.0 && git push --tags`. Before v0.1.0, a
  `v0.0.1-rc.1` tag tests the pipeline end to end (the workflow marks tags
  with a `-` suffix as pre-releases and skips the Homebrew job for them).

## Phase 6: v0.1.0

- Tag a release candidate, install it via the in-repo tap and via mise on both Macs.
- Switch `auth-env` in the dotfiles to `pmp env`/`pmp run` with
  `--skip-unavailable`.
- Tag `v0.1.0`.

## Roadmap after v0.1

- `keychain://` (darwin), `secret-service://` (linux).
- Speed: SDK first for 1Password, `bw serve`, an in-memory agent.
- `--export-session`, `op inject`-style templating.
- macOS signing and notarisation.
- Fish output (contributions welcome).
