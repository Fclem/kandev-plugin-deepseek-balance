# Prompt-input DeepSeek balance pill

## Problem

The prompt-input placement regressed in three ways: its detail panel is not reliably reachable by hover, its visual treatment no longer matches the preferred pre-PR1 DeepSeek branding, and it always shows the balance amount even when the account is healthy.

## Requirement

The prompt-input placement MUST retain the same hover, focus, click, and mouse-transition detail panel behavior as the task top-right placement. The panel MUST remain reachable while moving from the trigger into the panel.

The prompt-input trigger MUST show the balance amount only when the primary total is below the configured `warn_below` threshold. At or above the threshold it shows the branded icon without the amount. The task top-right placement keeps its existing amount behavior.

The trigger and panel branding MUST use a DeepSeek whale logo when a stable inline SVG asset is available. If the official asset cannot be embedded safely in the no-build bundle, use the uppercase `DS` fallback rather than the current lowercase `Ds` monogram.

## Verification

Bundle tests MUST cover prompt hover opening, bridge entry without closing, click/focus opening, healthy prompt-input amount suppression, and low-balance amount visibility. The existing task top-right amount and settings-link behavior MUST remain unchanged. This plugin repository has no Playwright harness; the real browser smoke test runs through the Kandev development instance on a LAN-accessible bind.
