# DeepSeek balance configuration link

## Problem

When the DeepSeek balance panel has no configured API key, its guidance names the
Settings > Plugins location as plain text. Users must leave the task surface and
find the plugin configuration page manually.

## Requirement

The balance panel's configuration guidance MUST expose the DeepSeek API Balance
plugin settings destination as an accessible link. Activating the link MUST
navigate to the host's per-plugin settings route without a full page reload.

The destination is the host route for the manifest plugin id:
`/settings/plugins/kandev-plugin-deepseek-balance`.

## Behavior

- In the `unconfigured` panel state, the `Settings → Plugins → DeepSeek API
  Balance` guidance is rendered as a link.
- In the error state without a retained balance snapshot, the same settings
  guidance is rendered as a link.
- The link has the exact settings-route `href` for normal browser semantics and
  delegates activation to `host.navigate` for soft SPA navigation.
- Existing environment-variable guidance, panel copy, refresh behavior, and
  balance rendering remain unchanged.
- The link is keyboard accessible and retains visible link styling consistent
  with the panel's muted guidance text.

## Verification

The bundle test suite MUST assert the link destination and that activating it
calls `host.navigate` with the settings route. Existing guidance assertions MUST
remain green. The bundle syntax check and the repository's `make test` target
cover the changed plugin surface; this repository has no Playwright test harness.
