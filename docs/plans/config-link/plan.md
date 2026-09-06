# Config-link and prompt-pill implementation plan

## Completed config-link scope

The hand-written plugin bundle links configuration guidance in the balance panel
to `/settings/plugins/kandev-plugin-deepseek-balance` and uses `host.navigate`
for SPA navigation. The existing Node/vm tests cover that behavior.

## Continuation scope

Fix the reported prompt-input regressions without changing task top-right
behavior:

1. Make the prompt-input trigger's hover/focus/click panel reliably open and
   keep the panel reachable across the transparent hover bridge.
2. Suppress the prompt-input amount at or above `warn_below`; show it below the
   threshold. Keep the branded trigger visible in both cases.
3. Replace the current lowercase `Ds` monogram with a simple stable DeepSeek
   whale SVG if the official shape can be embedded in the no-build bundle;
   otherwise use an uppercase `DS` fallback. Preserve the icon in both
   placements and retain all tone states.
4. Add focused Node/vm tests for prompt hover bridge behavior and threshold
   display before editing production code.

## TDD order for continuation

1. Add failing prompt-input tests: healthy amount hidden, low amount shown,
   hover opens the detail panel, and entering the fixed panel cancels close.
2. Run `node --test test/bundle.test.mjs` and confirm those assertions fail.
3. Implement the smallest prompt rendering and interaction changes.
4. Re-run syntax and bundle tests; refactor only while green.
5. Run `make fmt`, then the repository-supported checks (`make test`, `make
   vet`; `make typecheck test lint` is not defined in this plugin Makefile).
6. Package the plugin, start an isolated Kandev dev instance bound to LAN
   interfaces, and smoke-test the actual prompt surface.
7. Commit all continuation changes with a Conventional Commits message.

## Acceptance criteria

- Configuration links remain unchanged and functional.
- Prompt hover/focus/click opens the existing detail panel.
- Moving from the prompt trigger into the panel does not close it.
- Healthy prompt balances show only the whale/DS icon; low balances show the
  amount as well.
- The visual icon is the selected whale SVG or uppercase `DS` fallback, not
  the current lowercase `Ds` monogram.
