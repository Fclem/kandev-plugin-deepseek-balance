# kandev-plugin-template

A starter template for building a [kandev](https://github.com/kdlbs/kandev)
**native-UI plugin** — its own git repo, packaged into a versioned tarball and
installed against a running kandev instance. Click **“Use this template”** on
GitHub (or copy this repo) to bootstrap your own plugin.

It is a small, complete example of the core plugin surfaces, wired together so
you can delete what you do not need rather than assemble it from scratch:

- **Native nav item + route** — `ui/bundle.js` adds a sidebar entry that opens
  `/template`, a page rendered natively inside the kandev SPA (not an iframe)
  using the host's own React instance.
- **A page built from host components** — one `Card` containing a `Popover`
  (host-positioned, no `getBoundingClientRect` math), a `Progress` bar, a
  `Table` that swaps to `Empty` when there's nothing to show, and `Kbd` for a
  keyboard hint. Each part is independent: delete the ones you don't want.
- **Toasts** — the page's Clear button calls `host.toast.success` and
  `host.toast.error`. The host mounts the single `<Toaster/>`, so there is
  nothing to render and it works from anywhere, modals included.
- **Locale-aware timestamps** — `host.utils.formatRelativeTime` renders each
  row's "seen" column through `Intl.RelativeTimeFormat`, and `host.utils.cn`
  merges conditional classes the same way the host components do.
- **Live theme** — `host.onThemeChange` keeps a readout in the popover current
  when the user flips light/dark.
- **Chat toolbar action** — a component registered into the `chat-input-actions`
  slot renders an icon button in the chat composer toolbar, with the current
  `{ sessionId, taskId, taskTitle }` as `slotProps`.
- **Live WS-driven page** — a `registerWsHandler("task.created", ...)`
  handler updates module state that the page re-renders from, live, with no
  reload.
- **Backend event handling with Host state** — `OnEvent` counts `task.created`
  deliveries in a persistent counter via the `Host.GetState`/`SetState` round
  trip, so restarts don't reset it.
- **Backend webhook** — `HandleWebhook` answers the `ping` webhook kandev
  proxies to the plugin, building its reply from the operator settings. Its
  `access:` is declared explicitly — see
  [Webhook access](#webhook-access-declare-it) below.
- **Operator settings (`config_schema`)** — a `greeting` string and a secret
  `api_token`, rendered as a form at **Settings > Plugins > Template Plugin**
  and read by the plugin process via `host.GetConfig(ctx)`. Secret fields are
  vault-stored and masked everywhere outside the plugin process.

Source-control providers need several newer hooks that would swamp this default
starter. The compile-tested [provider-neutral source-control recipe](recipes/source-control/README.md)
shows repository search/paging/inspection/branches/create, task Link and unlink,
review status and responsive Review UI, composer `#` references, cancellation,
and unload fencing. It is opt-in and intentionally excluded from the generated
package until you copy its declarations and adapters into your plugin.

## Make it yours

The plugin **id** appears in four places that must stay in sync. Rename all of
them from `kandev-plugin-template` to your own id (e.g. `kandev-plugin-acme`):

1. `manifest.yaml` — `id`, plus `display_name` / `description` / `author`.
2. `go.mod` — the `module` line.
3. `Makefile` — `BIN` and `PKG_OUT` (and `VERSION` to match the manifest).
4. `ui/bundle.js` — the id passed to `window.registerKandevPlugin(...)`.

Then trim the scaffolding: drop the webhook / event / config blocks in
`manifest.yaml` you don't use, delete the matching handlers in
`server/plugin.go`, and keep only the `registry.register*` calls in
`ui/bundle.js` your plugin actually contributes. Update `server/plugin_test.go`
to cover what remains.

If you are building a Git provider, opt into `recipes/source-control/` as one
cohesive slice. Do not activate only its UI or only its manifest fragment:
provider ownership, declared actions, backend optional interfaces, and frontend
registrations are one contract.

The page in `ui/bundle.js` is deliberately built from independent parts —
`AboutPopover`, the `Progress` block, `RecentTasksTable`, `EmptyState`, the
Clear button — so you can delete any of them without unpicking the others.
Adjust `min_kandev_version` in `manifest.yaml` to match whatever you keep.

## Use the host's React — and the host's recharts

`initialize(registry, host)` hands you `host.React` (and `host.jsx`, an alias
for `host.React.createElement`). Use it. **Never import or bundle your own
React**: a second React instance has its own hook dispatcher and context
registry, so host components rendered inside your tree lose their providers,
refs break, and `asChild` stops composing.

The same hazard applies to **recharts**, which is why the host exposes
`ChartContainer`, `ChartTooltip`, `ChartTooltipContent`, `ChartLegend`,
`ChartLegendContent` and `ChartStyle` through `host.ui` rather than leaving you
to install it: recharts resolves its tooltips and legends through its own React
context and renders them into portals, so a bundled second copy splits exactly
what the host copy is holding. It costs nothing to use the host's — recharts is
already a dependency of the app. The same reasoning covers every
Radix/portal/context-based package. Pure-React libraries (`@tabler/icons-react`,
say) bundle fine, but this template ships hand-drawn inline SVG so it needs no
bundler at all.

`host.ui` is much broader than what this template uses — `Accordion*`,
`Collapsible*`, `Select*`, `Tabs*`, `Sheet*`, `Pagination*`, `ScrollArea`,
`Skeleton`, `Switch`, `Spinner`, `TooltipProvider`, plus kandev's own
`PageTopbar`, `Combobox` and `TaskCreateDialog`. Check it before hand-rolling a
component: the published plugins that hand-rolled progress bars and popovers
did so only because this template used to stop at `Button`. The authoritative
list is `PLUGIN_UI` in `apps/web/lib/plugins/host-api.ts`.

## Webhook access: declare it

`manifest.yaml`'s example webhook sets `access:` explicitly:

```yaml
webhooks:
  - key: "ping"
    method: "POST"
    access: "public"
```

The field is optional and currently defaults to `public`, but an open kandev PR
proposes inverting that default to `authenticated`. A manifest that omits it is
one whose security posture silently changes on a host upgrade; a manifest that
declares it means the same thing under either default. Choose per webhook:

| `access:`       | Caller                                           | Body limit |
| --------------- | ------------------------------------------------ | ---------- |
| `public`        | anonymous — GitHub, Slack, Stripe, any third party delivering to you. Verify the caller yourself (signature header, or a shared secret in a `secret: true` config field). | 4 MiB |
| `authenticated` | needs a kandev identity (session cookie or PAT) — your own scripts and CI. | 16 MiB |

If you want to call your plugin from your **own** frontend bundle, neither is
right: declare an `actions:` entry instead. The host authenticates and
authorizes those and passes a verified resource context to your backend, and
`host.api.fetch` is documented as MUST NOT target a public webhook path.

## Minimum host version

`manifest.yaml` declares `min_kandev_version: "0.86.0"` — the first release
carrying the `host.ui` primitives, `host.toast` and `host.utils` that
`ui/bundle.js` calls. A release host compares it against its own version at
install time and refuses an older one, so an operator gets a clear error
instead of a plugin that loads and then breaks on a missing API.

Raise it as you adopt newer host APIs; lower or drop it if you strip the bundle
back to the pre-0.86 surface (`Button`, `Card*`, `Tooltip*`).

The source-control recipe uses post-v0.87.1 contracts and therefore cannot use
this default `0.86.0` floor. Its README records the pinned source contract; set
an activated provider plugin's minimum to the first release containing those
hooks.

Two caveats worth knowing:

- The check only became load-bearing in 0.86.0. Earlier hosts parsed the field
  and ignored it, so it can't protect you from a host old enough to have that
  bug — a reason to declare it, not to skip it.
- It is **release-only by design**. A host built from a git checkout reports a
  git-describe version like `v0.87.1-27-g4705f1fd0`, which isn't a release
  version, so the gate is skipped and the install succeeds whatever floor you
  declare. A successful sideload onto your dev instance is not evidence that
  your floor is correct — check it against the kandev history instead
  (`git merge-base --is-ancestor <commit> <tag>`).

## How a plugin runs (gRPC subprocess, not HTTP)

kandev spawns the platform-matching binary from `runtime.executables` in
`manifest.yaml` as a subprocess and talks to it over a private gRPC connection
([hashicorp/go-plugin](https://github.com/hashicorp/go-plugin)) — there is no
HTTP listen address, no shared secret, and no manual wiring: `pluginsdk.Serve`
in `server/main.go` owns the entire transport. The base interface has two RPCs
and gives you a `Host` handle back:

```go
type Plugin interface {
    OnEvent(ctx context.Context, e *Event) error
    HandleWebhook(ctx context.Context, req *WebhookRequest) (*WebhookResponse, error)
}
```

`server/plugin.go`'s `templatePlugin` embeds `pluginsdk.UnimplementedPlugin`
(a no-op default for both RPCs, plus `Host()`/`SetHost()` accessors) and
overrides what it needs. `server/main.go` is just
`pluginsdk.Serve(&templatePlugin{})`.

Newer features are additive optional interfaces. For example, the
source-control recipe implements `pluginsdk.ActionHandler`,
`pluginsdk.EntityReferenceSearcher`, and
`pluginsdk.EntityReferenceAuthorizer`; a provider that needs transient Git
credentials can separately implement `GitCredentialResolver` and
`GitCredentialBinder`. Embedding the no-op base preserves the minimal plugin.

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
└── kandev-plugin-template/   # this repo
```

Note the module root is `kandev/apps/backend`, not the repo root — `kandev` is
a monorepo and the Go backend (including `pkg/pluginsdk`) lives one level down.
Adjust the `replace` path if your layout differs. Once `pkg/pluginsdk` ships as
a standalone, versioned module, this repo will drop the `replace` and pin a
real version instead.

The frontend recipe follows the same temporary source-checkout model through
`@kandev/plugin-sdk` in `package.json`. It is a runtime-free type dependency:
the recipe uses `import type`, and the default `ui/bundle.js` remains a
dependency-free ES module. CI pins both SDK contracts to the same Kandev source
revision.

## Layout

```
manifest.yaml          # plugin manifest — id, capabilities, runtime.executables, ui.bundle, config_schema
server/
  main.go              # pluginsdk.Serve wiring — no flags, no HTTP, no secrets
  plugin.go            # templatePlugin: OnEvent / HandleWebhook
  plugin_test.go       # tests against a fake Host, no subprocess spawn needed
ui/
  bundle.js            # hand-written, no-build ES module — the plugin's frontend half
recipes/source-control/
  manifest.fragment.yaml
  server/              # provider-neutral backend ports and contract tests
  ui/                  # typed native registrations and lifecycle tests
package.json           # recipe-only TypeScript tests/types; no production bundle
tsconfig.recipes.json  # strict public-SDK contract check
```

`ui/bundle.js` is hand-written, dependency-free ES module JavaScript. There is
no build step: it ships byte-for-byte inside the package tar.gz, and kandev
serves it directly. Edit the file and repackage — nothing else to run.

## Build and test

Install the recipe-only development dependencies once with
`npm ci --ignore-scripts`; nothing from `node_modules` enters the plugin
package.

```sh
make build               # go build -o bin/... ./server/...
make test                # base + recipe Go/TypeScript tests
make vet                 # base + recipe Go vet
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
entry`. Pulling them in would force this template's `go.sum` to track every
dependency the kandev backend grows. Building the tool where it lives keeps
`go.sum` scoped to what your plugin actually imports.

## Install it against a running kandev

Either through the UI (**Settings > Plugins > Install plugin**, URL or file
upload), or directly:

```sh
curl -F package=@kandev-plugin-template-0.1.0.tar.gz \
  http://localhost:<kandev-port>/api/plugins/install
```

kandev verifies `checksums.txt`, validates the manifest, extracts the package,
spawns the host-matching binary, and — once the go-plugin handshake completes —
marks the plugin active. Sideloaded plugins register **disabled/unverified**;
enable yours in **Settings > Plugins** (the `plugins` feature flag must be on).
Reinstalling the same version returns 409 — bump `version` in `manifest.yaml`.

## Publish a release

Pull requests run `.github/workflows/ci.yml` (tidy, format, vet, and test) and
`.github/workflows/build.yml` (host build plus a five-platform package). Push a
tag that matches the manifest version to run `.github/workflows/release.yml`:
it repeats verification, cross-compiles all platforms, packs the tarball, and
creates a GitHub Release with the two assets the kandev
[marketplace](https://github.com/kdlbs/kandev/blob/main/docs/public/plugins-marketplace.md)
install pipeline expects:

- `<id>-<version>.tar.gz` — the plugin package (with its own internal
  `checksums.txt` verified on install), and
- `checksums.txt` — the package's internal file checksums, extracted from the
  tarball for inspection and marketplace tooling.

```sh
# bump VERSION in Makefile + version in manifest.yaml first, then:
git tag v0.1.0
git push origin v0.1.0
```

The workflows check out the kandev monorepo as a sibling so the local Go and
TypeScript SDK paths resolve (see "Developing against the SDK"). They pin one
source revision for reproducible provider contracts; advance that pin
deliberately and rerun both contract suites when adopting a newer SDK.

## License

MIT — see [LICENSE](LICENSE). This template is meant to be copied and made your
own; your resulting plugin can carry whatever license you choose.
