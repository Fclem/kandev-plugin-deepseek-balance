# Task 02: Fix prompt-input balance pill regressions

## Inputs

- Requirement: `docs/specs/prompt-pill-regressions.md`
- Plan: `docs/plans/config-link/plan.md`
- UI source: `ui/bundle.js`
- Tests: `test/bundle.test.mjs`

## Work order

- Add red tests for prompt-input threshold rendering and hover-panel bridge
  behavior.
- Use the prompt action response's `warn_below` and primary total to hide the
  healthy amount while preserving the low-balance amount.
- Make prompt panel positioning and event ownership robust when moving from the
  trigger into the panel.
- Restore DeepSeek branding with an inline whale SVG when safe; otherwise use
  uppercase `DS` as the explicit fallback.

## LAN verification

Package the updated plugin, install it into the isolated Kandev dev instance,
and launch the web/backend pair bound to LAN interfaces. Exercise a prompt
session with `display_prompt_input` enabled, first at a healthy balance and then
below the threshold.

## Completion criteria

All bundle tests pass, the actual prompt-input surface opens and retains its
hover panel, healthy and low threshold states display as specified, and the
changes are committed with a Conventional Commit.
