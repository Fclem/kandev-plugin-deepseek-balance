# Task 01: Add the balance-panel configuration link

## Inputs

- Requirement: `docs/specs/config-link.md`
- Plan: `docs/plans/config-link/plan.md`
- UI source: `ui/bundle.js`
- Tests: `test/bundle.test.mjs`

## Work

- Add a link to the plugin settings route in every configuration guidance path
  in `panelBody`.
- Preserve the route in `href` and call `host.navigate` from the click handler
  after preventing the browser's default navigation.
- Add behavior-first tests for the unconfigured and no-snapshot error panels.

## Validation

- Red: new tests fail before the source change because no settings anchor exists.
- Green: `node --check ui/bundle.js` and `node --test test/bundle.test.mjs` pass.
- Repository checks: run the available Make targets in the plan's stated order
  and report any target absent from this plugin repository.

## Completion criteria

The panel exposes a keyboard-accessible settings link in both configuration
messages, navigation remains SPA-native, and the full existing bundle suite is
green.
