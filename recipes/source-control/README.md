# Provider-neutral source-control recipe

This is an opt-in, compile-tested boundary for Git-provider plugins. It shows
how one provider participates in Kandev's native repository, task Link, review,
and composer-reference surfaces without teaching an Acme-specific API.

The default template deliberately does not activate or package this directory.
`make package` copies only `manifest.yaml`, `ui/`, and the runtime binaries.
Copy the pieces you need, supply concrete provider adapters, merge the manifest
fragment, and raise `min_kandev_version` before publishing.

Contract references at the pinned source revision: [plugin authoring](https://github.com/kdlbs/kandev/blob/f218880ecbaa3d019d65b5d84fca6bdf160eced6/docs/public/plugins-authoring.md),
[manifest](https://github.com/kdlbs/kandev/blob/f218880ecbaa3d019d65b5d84fca6bdf160eced6/docs/public/plugins-manifest.md),
[frontend SDK](https://github.com/kdlbs/kandev/blob/f218880ecbaa3d019d65b5d84fca6bdf160eced6/apps/packages/plugin-sdk/src/index.ts),
and [backend SDK extensions](https://github.com/kdlbs/kandev/blob/f218880ecbaa3d019d65b5d84fca6bdf160eced6/apps/backend/pkg/pluginsdk/plugin.go).

## What belongs where

| Layer | File | Responsibility |
| --- | --- | --- |
| Provider-neutral backend boundary | `server/recipe.go` | Authenticated actions, verified context, pagination, immutable identity, branch lookup, create/link/unlink outcomes, normalized review JSON |
| Composer bridge | `server/references.go` | Bounded search plus fresh, fail-closed authorization for both search and submission |
| Native frontend registrations | `ui/register.ts` | Repository provider, host-owned Link dialog, review snapshots, semantic task status, shared Review detail surface |
| Async ownership | `ui/review-store.ts` | Per-key ordering and unload epochs so stale work cannot publish |
| Declarations | `manifest.fragment.yaml` | Action scopes/body limits, provider ownership, reference source, least-privilege Host capabilities |

Keep provider HTTP clients, OAuth setup, credential refresh, webhooks, durable
association storage, and watch/reconciliation loops in concrete adapters. Those
concerns vary substantially between Git hosts and obscure the public hooks.

## Wire it in

Embed or delegate the backend extension from the value passed to
`pluginsdk.Serve`. The concrete adapters implement the narrow ports and own all
provider-specific URLs, pagination tokens, authentication, and error mapping.

```go
type plugin struct {
    pluginsdk.UnimplementedPlugin
    sourcecontrol.Extension
}

func newPlugin(hostBackedAdapters adapters) *plugin {
    return &plugin{Extension: sourcecontrol.Extension{
        ProviderID:           "acme",
        ReferenceSource:      "acme_change_requests",
        Repositories:         adapters.repositories,
        RepositoryDetails:    adapters.repositoryDetails,
        AttachedRepositories: adapters.attachedRepositoryResolver,
        ChangeRequests:       adapters.changeRequests,
        Associations:         adapters.associations,
        Reviews:              adapters.reviews,
        References:           adapters.references,
    }}
}
```

Register the frontend recipe from your bundle and retain its lifecycle handle:

```ts
let sourceControl: SourceControlRecipeLifecycle | undefined;

window.registerKandevPlugin("kandev-plugin-acme", {
  initialize(registry, host) {
    sourceControl = registerSourceControlRecipe(registry, host, {
      providerId: "acme",
      label: "Acme",
      changeRequestNoun: "change request",
      supportsDraft: true,
      parseReference: parseAcmeChangeRequestReference,
      toChangeRequestDetail: toAcmeChangeRequestDetail,
    });
  },
  destroy() {
    sourceControl?.destroy();
    sourceControl = undefined;
  },
});
```

`toChangeRequestDetail` is intentionally the one provider-specific presentation
adapter. The public SDK currently types `host.ui.ChangeRequestDetail` as an
opaque host component, so keep this function small and covered by an installed-
package smoke test. Use the same host component for desktop and mobile; the
host selects the surrounding dialog/drawer and owns responsive navigation.

## Contract rules demonstrated

### Repositories and create

- Search and page on the server. The frontend forwards `query`, opaque
  `cursor`, `limit`, and `AbortSignal`; the backend clamps the limit and binds
  each cursor to query, connection scope, and immutable repository identity.
- Treat `matchesURL` only as a cheap hint. Authenticated, workspace-scoped
  `inspectURL` is ownership authority and returns `null` when not owned.
- Resolve branch descriptors again on the server by connection scope plus the
  provider's immutable repository ID. Clone URLs and names are display/routing
  data, never credentials or authority.
- Create uses verified workspace/task/session/repository/head-branch context.
  The body may supply only title, description, destination, and draft state.
- Remote creation and local association are two effects. If creation succeeds
  but linking fails, return the URL with `linked: false` and a safe
  `association_error`; do not turn a created remote review into a retryable
  create failure that could duplicate it.

### Link, unlink, and review identity

- Register a child of Kandev's native **Link** menu and use
  `host.openTaskLinkDialog`; it supplies validation state, cancellation,
  success/error toasts, and desktop/mobile presentation.
- Resolve a pasted display reference server-side before storing it.
- Persist and unlink by the complete immutable tuple
  `(provider ID, connection scope, repository ID, change-request number)`.
  Human-readable paths, clone URLs, titles, and `reviewKey` may change.
- Refresh task and workspace snapshots after mutations. A failed best-effort
  refresh does not rewrite a successful mutation outcome. Cancellation still
  reaches that refresh, but once the mutation response is known, its outcome
  wins so a caller is not encouraged to create or link a duplicate.

### Review surfaces

Publish semantic `taskStatus`: review state, pipeline state, individual checks,
approval counts, unresolved comments, and update time. Kandev uses it for the
sidebar/Kanban indicator, task top bar, composer CI chip, semantic colors, CI
popover, and mobile drawer. Do not publish CSS classes, choose status colors,
register duplicate chrome, or run another poller.

Keep `refresh(taskId)` cheap. Fetch files, diffs, commits, and comment bodies
only when Review opens. Feed both presentations through
`host.ui.ChangeRequestDetail` so desktop and mobile cannot drift.

### Composer `#` references

The manifest descriptor and `ReferenceSource` must match exactly. Search
returns bounded display candidates with an opaque provider-local ID. Candidate
selection is not authorization: `AuthorizeEntityReference` must perform a live
workspace/provider access check again for purpose `submission`. Errors,
timeouts, disconnects, and revoked access fail closed.

### Lifecycle and permissions

- Forward every frontend `AbortSignal`; pass every backend `context.Context`
  into provider calls and check cancellation before starting work.
- The snapshot store rejects out-of-order refreshes. `destroy()` increments its
  epoch, clears listeners/data, and fences every promise started before unload.
- Concrete adapters own clients, timers, watches, and goroutines. Tie them to a
  cancelable process/provider lifecycle, change connection epochs on credential
  replacement, and stop publishing after disconnect.
- Merge only declarations you use. `api_read` gates Host reads; `state` gates
  Host state. Action scopes authorize selectors but do not grant Host data API
  access. Never put tokens in browser payloads, repository descriptors,
  snapshots, logs, cursors, or action responses.

At this recipe's introduction, the contracts were present at Kandev source
commit `f218880ecbaa3d019d65b5d84fca6bdf160eced6` after release `v0.87.1`; no
release tag contained them yet. Pin development/CI to a known source contract,
then set `min_kandev_version` to the first release that contains it. Do not use
the base template's `0.86.0` floor for an activated provider recipe.

## Verification

```sh
go test ./recipes/source-control/server -count=1
npm run typecheck:recipes
npm run test:recipes
npm audit --audit-level=high
```

Tests cover server-side paging and cursor binding, verified create context,
partial create success, canonical link/unlink identity, cancellation, semantic
review normalization, stale/unload fencing, and submission-time
reauthorization. Also install the packaged plugin into a pinned compatible host
and smoke-test registration, one canceled request, desktop Review, mobile
Review, and `#` submission. Unit tests cannot prove manifest/host wiring.

Current host-contract gaps limit what this small recipe can compile-check:
`host.ui.ChangeRequestDetail` is intentionally typed as an opaque component,
and `ReviewPanel` receives no host-owned `AbortSignal`. A full lazy detail panel
must therefore own an `AbortController` in its component effect and abort it on
cleanup; an exported detail-props type and panel-scoped signal would make this
safer. The backend SDK also has no plugin shutdown callback, so long-lived
watches need a process-owned cancellation/connection-epoch adapter. This recipe
omits watches instead of implying that request contexts own them.

## Advanced follow-on recipes

Keep these separate rather than making every generated plugin carry them:

- OAuth/configuration UI, self-hosted origin discovery, and connection epochs.
- `GitCredentialResolver` plus `GitCredentialBinder`, secret-safe clone/fetch,
  rotation, and pre-environment origin refresh.
- Durable watches/webhooks, reconciliation, rate limits, and backoff.
- Lazy full diff/comment/reviewer detail, truncation, and provider error
  categorization/retry hints.
- Provider contract tests against a fake HTTP server and packaged-host E2E.
