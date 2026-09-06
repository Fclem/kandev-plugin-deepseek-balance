# DeepSeek API Balance for Kandev

Native [Kandev](https://github.com/kdlbs/kandev) plugin that shows DeepSeek API balance and credit breakdown in the task top bar or prompt toolbar.

The compact balance pill shows the primary currency total. Hovering, focusing, or clicking it opens details for total, granted, topped-up, every currency entry, availability, last update, and refresh.

The balance comes from DeepSeek's `GET /user/balance` endpoint. The configured `api_key` is stored as a Kandev secret; when unset, the plugin uses `DEEPSEEK_API_KEY`. Data reaches the UI only through the authenticated, workspace-scoped `balance.get` action.

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
└── kandev-plugin-deepseek-balance/   # this repo
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

Install `kandev-plugin-deepseek-balance-0.1.1.tar.gz` through **Settings → Plugins → Install plugin**, or with the operator API:

```sh
curl -F package=@kandev-plugin-deepseek-balance-0.1.1.tar.gz \
  http://localhost:8080/api/plugins/install
```

Kandev verifies the archive's internal `checksums.txt`, validates the manifest, and starts the binary matching the host platform.

## Configure

Note the Makefile runs `plugin-pack` with `cd $(KANDEV_SDK) && go run
./cmd/plugin-pack`, from inside the sibling kandev checkout, rather than as
`go run github.com/kandev/kandev/cmd/plugin-pack` from here. The second
spelling resolves plugin-pack's dependencies against *this* module's `go.sum`,
and plugin-pack reaches much further into the kandev backend than `server/`
does — so those entries are missing and packaging fails with `missing go.sum
entry`. Pulling them in would force this repo's `go.sum` to track every
dependency the kandev backend grows. Building the tool where it lives keeps
`go.sum` scoped to what your plugin actually imports.

| Setting | Default | Behavior |
| --- | --- | --- |
| **DeepSeek API key** | empty | Secret used only by the plugin backend for `GET https://api.deepseek.com/user/balance`. |
| **Display · Task top right** | enabled | Shows the balance pill in the task session top bar. |
| **Display · Prompt input** | disabled | Shows the balance action beside Send in the prompt toolbar. |

Both display settings may be enabled simultaneously. The API key never reaches the browser.

## Minimum host version

`manifest.yaml` declares `min_kandev_version: "0.88.0"`, the first release carrying authenticated plugin actions required by this plugin.


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

## Submit to Kandev's official marketplace

After merging this repository and publishing a GitHub Release containing `kandev-plugin-deepseek-balance-<version>.tar.gz` plus `checksums.txt`, fork [`kdlbs/kandev`](https://github.com/kdlbs/kandev) and add:

```yaml
- id: kandev-plugin-deepseek-balance
  repo: Fclem/kandev-plugin-deepseek-balance
  categories: [analytics]
```

Open a PR against `plugin-registry/plugins.yaml`. Maintainers validate the release asset, manifest ID, package checksums, and metadata before inclusion. Marketplace curation does not make this a first-party plugin; keep `author: "Fclem"` unless Kandev maintainers transfer the repository to `kdlbs`.

See the [marketplace publishing guide](https://github.com/kdlbs/kandev/blob/main/docs/public/plugins-marketplace.md#publishing-a-plugin).
