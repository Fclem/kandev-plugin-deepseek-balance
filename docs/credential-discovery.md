# DeepSeek credential discovery design

Status: proposed; not implemented by the current release.

## Goal

After installation, detect DeepSeek API keys already available to the Kandev plugin process or stored by supported coding harnesses. Present safe candidates to the operator so they can reuse one instead of entering the same key again.

Discovery is not import. The plugin must never copy, persist, validate, or expose a discovered secret until the operator explicitly selects it.

## Product flow

1. The plugin backend starts after installation and runs a bounded, read-only discovery pass when neither plugin configuration nor the plugin secret store contains a key.
2. Plugin settings show either no candidates or a list containing source name, location, and a masked fingerprint. The browser never receives key bytes.
3. The operator selects **Use this key**. The backend resolves the opaque candidate handle, validates that key with DeepSeek's balance endpoint, and stores it in the plugin's namespaced secret store.
4. Explicit `api_key` plugin configuration continues to take precedence over an adopted secret. `DEEPSEEK_API_KEY` remains the final runtime fallback.
5. Settings provide **Search again**. Discovery is not permanently disabled because another harness may be installed later.

Kandev currently starts a plugin after installation but exposes no plugin-specific install callback. “On install” therefore means the first plugin process start after installation. If Kandev adds an install lifecycle hook later, it can trigger the same discovery service without changing adapters.

## Supported sources

Each source is an explicit, versioned adapter. Do not recursively scan the home directory, shell history, session transcripts, project files, or arbitrary `.env` files.

### Environment

Read only `DEEPSEEK_API_KEY` from the plugin process environment. This is already a supported runtime source. It should be displayed as `Environment` with no filesystem location.

### Oh My Pi (OMP)

OMP currently stores local credentials in `<agent-dir>/data/agent.db`, normally `~/.omp/agent/data/agent.db`. API-key credentials are rows in `auth_credentials`; the `data` JSON object contains `key`. The adapter must:

- open the SQLite database read-only and immutable where supported;
- require the expected table and columns before querying;
- select only enabled `deepseek` API-key rows;
- resolve literal keys only; never execute OMP `!command` configuration values;
- skip local database access when OMP is configured for a remote auth broker rather than attempting to contact that broker.

OMP also supports `DEEPSEEK_API_KEY`; the environment adapter deduplicates that case.

Source references:

- [OMP auth-storage discovery](https://github.com/can1357/oh-my-pi/blob/main/packages/ai/src/auth-broker/discover.ts)
- [OMP SQLite credential store](https://github.com/can1357/oh-my-pi/blob/main/packages/ai/src/auth/sqlite-credential-store.ts)
- [OMP directory helpers](https://github.com/can1357/oh-my-pi/blob/main/packages/utils/src/dirs.ts)

### Pi coding agent

Pi stores credentials in `<agent-dir>/auth.json`, normally `~/.pi/agent/auth.json`; `PI_CODING_AGENT_DIR` overrides the agent directory. The adapter must parse the documented credential object, select only the `deepseek` entry with `type: "api_key"`, and reject unknown shapes rather than guessing.

Source reference: [Pi file auth storage](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/src/core/auth-storage.ts).

### OpenCode

OpenCode documents credentials at the platform data directory, normally `~/.local/share/opencode/auth.json` on Linux. The adapter must select only the `deepseek` provider's API credential and reject unknown shapes.

Provider configuration files such as `~/.config/opencode/opencode.json` and `.jsonc` can refer to environment variables or provider options. The initial adapter should not parse them: doing so creates precedence, JSONC, command-expansion, and project-scope ambiguity without adding coverage beyond the environment and credential-store adapters.

Source reference: [OpenCode provider and credential documentation](https://opencode.ai/docs/providers/).

### Other DeepSeek coding clients

Add an adapter only after identifying a concrete product, supported versions, and an authoritative credential schema. There is no generic “DeepSeek codes” filesystem scan. A broad search risks collecting unrelated production secrets and private source material.

## Backend model

```go
type CredentialCandidate struct {
    Handle      string // random, process-local lookup token
    Source      string // stable adapter ID
    Location    string // display-safe path or "environment"
    Fingerprint string // truncated SHA-256; never key prefix/suffix
}

type CredentialSource interface {
    ID() string
    Discover(context.Context) ([]discoveredCredential, error)
}

type discoveredCredential struct {
    Candidate CredentialCandidate
    Secret    []byte // backend-only, short-lived
}
```

Discovery runs adapters concurrently under a shared timeout, then deduplicates candidates by full SHA-256 digest. The digest and secret never cross the plugin HTTP boundary. Candidate handles expire on process restart and after a short TTL; rescan replaces the candidate set. Secret buffers are discarded immediately after expiry or adoption.

Adapter errors are isolated and returned as source-level status (`unavailable`, `unsupported schema`, or `permission denied`) without filesystem contents or secret-bearing parse errors.

## Storage and host contract

Adopted keys should use the Kandev plugin SDK secret store under a namespaced key such as `deepseek_api_key`. This requires adding the manifest `secrets` capability. It avoids rewriting the operator-managed `api_key` config field, for which the current plugin host interface exposes reads but no configuration write API.

Effective-key precedence:

1. explicit secret `api_key` configuration;
2. operator-adopted plugin secret;
3. process `DEEPSEEK_API_KEY`.

If product requirements demand that adoption populate the visible `api_key` field, Kandev needs an authenticated plugin-config write API or install hook first. The plugin must not write Kandev's database directly.

## Security constraints

- No silent import, even when exactly one candidate exists.
- No key bytes, prefixes, suffixes, hashes, or upstream error bodies in logs. UI fingerprints are independently keyed display identifiers or short SHA-256 digests; they are for deduplication, not authentication.
- Resolve paths from the target user's home/config/data directories, not from hard-coded Linux paths. Respect `PI_CODING_AGENT_DIR` and documented harness overrides.
- Refuse non-regular files, oversized files, and symlinks escaping an allowed harness directory. Open files without following symlinks where the platform permits and verify ownership/permissions before reading.
- Open SQLite read-only; never migrate, checkpoint, or lock another harness's database.
- Never execute config substitutions, commands, plugins, or harness binaries during discovery.
- Do not validate candidates over the network until operator adoption. Validation uses only `GET https://api.deepseek.com/user/balance` with existing timeouts and error redaction.
- Remote Kandev executors can only inspect their own filesystem and environment. The UI must say where discovery ran; it must not imply that a remote worker scanned the user's workstation.

## Plugin endpoints and UI

Prefer authenticated plugin actions over public webhooks:

- `discover-credentials`: start/rescan and return metadata-only candidates;
- `adopt-credential`: accept one opaque handle, validate server-side, persist through `SetSecret`, invalidate all handles;
- `forget-adopted-credential`: delete only the plugin-owned secret.

The `plugin-settings` component displays scan status and candidate actions. Balance display components continue to consume only the balance report and never credential-discovery payloads.

## Acceptance criteria

- A fresh install with no plugin key reports candidates from supported sources without persisting or transmitting secret bytes.
- Selecting a valid candidate stores it in the plugin secret store and enables the balance display after restart.
- Canceling or ignoring discovery changes no state.
- Explicit plugin configuration overrides adopted and environment keys.
- Duplicate keys from multiple sources appear once with all source labels.
- Malformed, oversized, symlinked, inaccessible, and unsupported-version stores fail closed per adapter without blocking other sources.
- Logs, action responses, frontend state, and error messages contain no discovered secret material.
- Linux, macOS, Windows, remote-executor, OMP-broker, and custom Pi-directory behavior have contract tests.
