# kandev-deepseek-credits

DeepSeek API balance and remaining credits in the session top bar, as an
official [kandev](https://github.com/kdlbs/kandev) **native-UI plugin** — its
own git repo, packaged into a versioned tarball and installed against a
running kandev instance.

A pill in the session top bar (beside the CPU/DB metrics) shows the account's
remaining DeepSeek balance — a DeepSeek monogram chip plus the formatted total
of the primary currency, e.g. `¥104.32` or `$5.20`. Hovering the pill
(desktop) or clicking/tapping it (all surfaces) opens a panel with the full
breakdown: total, granted/topped-up components, every currency entry,
`is_available` status, last-updated time, and a Refresh control. The pill
turns amber below the `warn_below` threshold and muted coral while DeepSeek
reports `is_available: false`.

The balance comes from DeepSeek's `GET /user/balance` endpoint using the
operator's DeepSeek API key — from the plugin settings (`api_key`, a
`secret: true` vault-stored field) or, when unset, from the
`DEEPSEEK_API_KEY` environment variable the plugin subprocess inherits from
kandev. Data reaches the UI only through the authenticated, workspace-scoped
`balance.get` action; the plugin declares no webhooks, so no balance data is
reachable over an unauthenticated route.

## Layout

```
manifest.yaml          # plugin manifest — id, runtime.executables, ui.bundle, config_schema, actions
server/
  main.go              # pluginsdk.Serve wiring — no flags, no HTTP, no secrets
  plugin.go            # the plugin: balance.get action + warm-snapshot poller
  balance.go           # DeepSeek balance client with a strict error taxonomy
  plugin_test.go       # action + poller tests against a fake Host, no subprocess spawn
  balance_test.go      # httptest-based client tests
ui/
  bundle.js            # hand-written, no-build ES module — chat-top-bar pill + panel
test/
  bundle.test.mjs      # node --test bundle tests against a host mock
```

`ui/bundle.js` is hand-written, dependency-free ES module JavaScript. There is
no build step: it ships byte-for-byte inside the package tar.gz, and kandev
serves it directly. Edit the file and repackage — nothing else to run.

## Developing against the SDK

`pkg/pluginsdk` is not published as its own module yet, so `go.mod` here uses a
local `replace`:

```
replace github.com/kandev/kandev => ../kandev/apps/backend
```

This assumes your plugin repo is checked out as a **sibling** of the `kandev`
monorepo:

```
some-dir/
├── kandev/                   # https://github.com/kdlbs/kandev, Go module at apps/backend/
└── kandev-plugin-deepseek-credits/   # this repo
```

Note the module root is `kandev/apps/backend`, not the repo root — `kandev` is
a monorepo and the Go backend (including `pkg/pluginsdk`) lives one level down.
Adjust the `replace` path if your layout differs (e.g. a symlink named
`kandev` pointing at a differently named checkout). Once `pkg/pluginsdk` ships
as a standalone, versioned module, this repo will drop the `replace` and pin a
real version instead.

## Build and test

```sh
make build               # go build -o bin/... ./server/...
make test                # backend Go tests + ui/bundle.js syntax check + bundle tests
make vet                 # go vet ./server/...
make verify-package-host # validate a host-only tarball and checksums
```

> Note: bare `go build ./server/...` (no `-o`) fails with `build output
> "server" already exists and is a directory` — Go's default output name for a
> lone main package is the last path element ("server"), which collides with
> the `server/` source directory. Always pass `-o`, run `go build .` from
> inside `server/`, or use `make build`. `go vet`/`go test` are unaffected.

## Package it

```sh
make package        # cross-compiles linux/darwin (amd64+arm64) + windows/amd64,
                    # then packs manifest + ui/ + binaries into a versioned .tar.gz

make package-host   # host platform only — faster local iteration
make verify-package # build + validate the five-platform archive
```

Both stage `manifest.yaml` + `ui/` alongside the freshly built
`server/plugin-<goos>-<goarch>[.exe]` binaries, then pack the tree with
kandev's `cmd/plugin-pack`, which computes `checksums.txt` and writes the
tarball.

Note the Makefile runs `plugin-pack` with `cd $(KANDEV_SDK) && go run
./cmd/plugin-pack`, from inside the sibling kandev checkout, rather than as
`go run github.com/kandev/kandev/cmd/plugin-pack` from here. The second
spelling resolves plugin-pack's dependencies against *this* module's `go.sum`,
and plugin-pack reaches much further into the kandev backend than `server/`
does — so those entries are missing and packaging fails with `missing go.sum
entry`. Pulling them in would force this repo's `go.sum` to track every
dependency the kandev backend grows. Building the tool where it lives keeps
`go.sum` scoped to what your plugin actually imports.

## Install it against a running kandev

Either through the UI (**Settings > Plugins > Install plugin**, URL or file
upload), or directly:

```sh
curl -F package=@kandev-deepseek-credits-0.1.0.tar.gz \
  http://localhost:<kandev-port>/api/plugins/install
```

kandev verifies `checksums.txt`, validates the manifest, extracts the package,
spawns the host-matching binary, and — once the go-plugin handshake completes —
marks the plugin active. Sideloaded plugins register **disabled/unverified**;
enable yours in **Settings > Plugins** (the `plugins` feature flag must be on).
Reinstalling the same version returns 409 — bump `version` in `manifest.yaml`
(and the lockstep `VERSION` in the Makefile).

## Minimum host version

`manifest.yaml` declares `min_kandev_version: "0.88.0"` — the first release
carrying authenticated plugin actions (`pluginsdk.HandleAction`, PR #2117),
which this plugin's `balance.get` action requires. A release host compares it
against its own version at install time and refuses an older one.

## How a plugin runs (gRPC subprocess, not HTTP)

kandev spawns the platform-matching binary from `runtime.executables` in
`manifest.yaml` as a subprocess and talks to it over a private gRPC connection
([hashicorp/go-plugin](https://github.com/hashicorp/go-plugin)) — there is no
HTTP listen address, no shared secret, and no manual wiring: `pluginsdk.Serve`
in `server/main.go` owns the entire transport. `server/plugin.go` embeds
`pluginsdk.UnimplementedPlugin` (a no-op default for the base RPCs, plus
`Host()`/`SetHost()` accessors) and overrides `SetHost` (to start the balance
poller) and `HandleAction` (the `balance.get` action).

## Use the host's React

`initialize(registry, host)` hands you `host.React` (and `host.jsx`, an alias
for `host.React.createElement`). Use it. **Never import or bundle your own
React**: a second React instance has its own hook dispatcher and context
registry, so host components rendered inside your tree lose their providers,
refs break, and `asChild` stops composing. The bundle renders only through
`host.jsx`, `host.ui`, and plain elements.

## Publish a release

Pull requests run `.github/workflows/ci.yml` (tidy, format, vet, test, and a
base-floor check compiling `server/` against the declared minimum Kandev SDK
`v0.88.0`), plus `.github/workflows/build.yml` (five-platform packaging).
Releases run from the Actions workflow dispatch on `main`: it calculates the
next SemVer, updates `manifest.yaml` / `Makefile` / `README.md` /
`CHANGELOG.md`, tags the release commit, builds the five-platform tarball with
`make verify-package`, and publishes the tarball + checksums as a GitHub
Release.
