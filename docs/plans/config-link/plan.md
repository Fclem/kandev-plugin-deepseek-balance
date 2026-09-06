# Config-link implementation plan

## Scope

Update the hand-written plugin bundle so configuration guidance in the balance
panel links to the installed plugin's native Settings > Plugins detail page.
No manifest, backend, route, or package contract changes are required.

## Design

1. Add one module-level settings route constant derived from the manifest plugin
   id.
2. Add a small panel helper that renders the settings destination as a native
   anchor with an `href`, muted/underlined styling, and an `onClick` handler
   that prevents a full reload and calls `host.navigate`.
3. Use that helper in both panel branches that tell an operator to check the
   plugin settings: the unconfigured state and the error-without-snapshot
   state.
4. Extend the existing Node/vm bundle tests with a host navigation spy and
   assertions for the anchor's href and click behavior.

## TDD order

1. Add the failing unconfigured-state link test and error-state link test.
2. Run `node --test test/bundle.test.mjs` and confirm the new assertions fail.
3. Implement the route constant, helper, and panel call sites.
4. Re-run the bundle test and syntax check; refactor only while green.
5. Run `make fmt` first, then `make typecheck test lint` if supported by the
   repository; otherwise run the documented plugin equivalents (`make test`,
   `make vet`) and record the unavailable targets.
6. Review the diff and commit with a Conventional Commits message.

## Acceptance criteria

- Both configuration guidance branches expose an accessible link.
- The link points to `/settings/plugins/kandev-plugin-deepseek-balance`.
- A click calls `host.navigate` with that route and does not submit or reload.
- Existing panel text and all unrelated balance behavior remain unchanged.
