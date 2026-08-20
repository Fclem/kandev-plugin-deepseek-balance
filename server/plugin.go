// Package main implements the kandev-deepseek-credits plugin backend: a
// warm-snapshot poller for DeepSeek's GET /user/balance plus the
// authenticated, workspace-scoped `balance.get` action. The action is the
// ONLY data path — the manifest declares no webhooks and no capabilities, so
// balance data is never reachable over an unauthenticated route.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/kandev/kandev/pkg/pluginsdk"
)

const (
	actionKeyBalanceGet  = "balance.get"
	configKeyAPIKey      = "api_key"
	configKeyPollMinutes = "poll_minutes"
	configKeyWarnBelow   = "warn_below"

	envAPIKey = "DEEPSEEK_API_KEY"

	// defaultPollMinutes is the background snapshot refresh interval; the
	// config floor (minPollMinutes) keeps a misconfig from hammering DeepSeek.
	defaultPollMinutes = 5.0
	minPollMinutes     = 1.0

	// defaultWarnBelow is the amber threshold in primary-currency units. The
	// plugin owns the default: GetConfig returns an empty map when the
	// operator never opened Settings, so the response always carries the
	// effective value (null only when unconfigured or pre-SetHost).
	defaultWarnBelow = 10.0

	// cooldownWindow bounds how often a refresh may force a DeepSeek round
	// trip, anchored on the last COMPLETED FETCH ATTEMPT (successful or
	// failed). The action body is attacker-controlled; without this, sequential
	// refreshes would burn the operator's rate quota. Singleflight dedupes
	// only concurrent fetches, so the attempt-anchored cooldown is what bounds
	// sustained amplification in a persistent error state.
	cooldownWindow = 5 * time.Second

	// Action response statuses (always delivered over HTTP 200).
	statusOK           = "ok"
	statusLoading      = "loading"
	statusUnconfigured = "unconfigured"
	statusError        = "error"
)

// balanceFetcher is the DeepSeek client seam; production uses realBalanceFetcher,
// tests inject a scripted fake.
type balanceFetcher interface {
	Fetch(ctx context.Context, apiKey string) (*Balance, error)
}

// realBalanceFetcher wraps the package-level fetchBalance client.
type realBalanceFetcher struct{}

func (realBalanceFetcher) Fetch(ctx context.Context, apiKey string) (*Balance, error) {
	return fetchBalance(ctx, apiKey)
}

// fetchOutcome is the result of one completed fetch attempt.
type fetchOutcome struct {
	snap *Balance
	at   time.Time
	err  *BalanceError
}

// inflightFetch lets a refresh JOIN an in-flight poll (singleflight) instead
// of starting a second DeepSeek round trip. done is closed when the fetch
// completes; every waiter then reads the stored outcome.
type inflightFetch struct {
	done chan struct{}
}

// plugin implements pluginsdk.Plugin (via UnimplementedPlugin) and the
// optional ActionHandler extension: the authenticated balance.get action.
//
// SetHost starts a background poller (once) that fetches immediately, then
// every poll_minutes, for the life of the plugin subprocess (reaped on
// process exit). The poller keeps a warm in-memory snapshot; a failed poll
// never evicts the last successful one.
type plugin struct {
	pluginsdk.UnimplementedPlugin

	// Seams injected for tests; production values set in newPlugin.
	fetch balanceFetcher
	now   func() time.Time

	// disablePoller keeps the background goroutine from starting in tests,
	// so the snapshot is built by explicit pollOnce calls instead.
	disablePoller bool
	pollerOnce    sync.Once

	// pollMu serializes fetch starts and guards the inflight pointer so the
	// ticker and concurrent refreshes never run DeepSeek twice.
	pollMu   sync.Mutex
	inflight *inflightFetch

	// mu guards the snapshot/error state and the lifecycle flags.
	mu                  sync.Mutex
	hostSet             bool
	initialPollInFlight bool // pending flag: set synchronously in SetHost, cleared by the first completed attempt
	snapshot            *Balance
	snapshotAt          time.Time // last SUCCESSFUL fetch (fetched_at)
	lastErr             *BalanceError
	lastAttempt         time.Time // last COMPLETED fetch attempt, success or failure (cooldown anchor)
}

var (
	_ pluginsdk.Plugin        = (*plugin)(nil)
	_ pluginsdk.ActionHandler = (*plugin)(nil)
)

func newPlugin() *plugin {
	return &plugin{
		fetch: realBalanceFetcher{},
		now:   time.Now,
	}
}

// SetHost stores the Host and starts the background poller on first injection.
// The initial-loading pending flag is set synchronously BEFORE the poller
// goroutine starts, so the loading window has a defined start from the moment
// SetHost runs and there is no gap in the classification from then on.
func (p *plugin) SetHost(h pluginsdk.Host) {
	p.UnimplementedPlugin.SetHost(h)
	p.mu.Lock()
	p.hostSet = true
	p.initialPollInFlight = true
	p.mu.Unlock()
	if p.disablePoller {
		return
	}
	p.pollerOnce.Do(func() { go p.pollLoop() })
}

// pollLoop refreshes the snapshot immediately, then every poll_minutes, for
// the life of the plugin subprocess.
func (p *plugin) pollLoop() {
	ctx := context.Background()
	p.pollOnce(ctx)
	for {
		timer := time.NewTimer(p.pollInterval(ctx))
		<-timer.C
		p.pollOnce(ctx)
	}
}

// pollOnce refreshes the snapshot, serialized by pollMu: while a fetch is in
// flight it JOINS it rather than starting a second round trip. While no key
// is configured it is a no-op — zero DeepSeek round trips — and clears the
// initial-loading flag so the action settles to unconfigured.
func (p *plugin) pollOnce(ctx context.Context) {
	key := p.apiKeyFromConfig(ctx)
	if key == "" {
		p.mu.Lock()
		p.initialPollInFlight = false
		p.mu.Unlock()
		return
	}
	p.startFetch(ctx, key)
}

// startFetch begins one fetch unless one is already in flight, in which case
// it JOINS it (singleflight): an action spans at most one 10 s client fetch,
// under the host's 15 s action deadline. It waits for the fetch (its own or
// the joined one) and returns the stored outcome. The in-flight fetch's done
// channel is closed once the outcome is stored, so every waiter — the starter
// and each joiner — observes the same result.
func (p *plugin) startFetch(ctx context.Context, key string) fetchOutcome {
	p.pollMu.Lock()
	if p.inflight != nil {
		done := p.inflight.done
		p.pollMu.Unlock()
		<-done
		return p.storedOutcome()
	}
	done := make(chan struct{})
	p.inflight = &inflightFetch{done: done}
	p.pollMu.Unlock()

	go func() {
		p.doFetch(ctx, key)
		p.pollMu.Lock()
		p.inflight = nil
		p.pollMu.Unlock()
		close(done)
	}()
	<-done
	return p.storedOutcome()
}

// storedOutcome reads the outcome of the just-completed fetch from the store.
// On failure the snapshot is the last successful one (retained, never
// evicted) and err the cached reason.
func (p *plugin) storedOutcome() fetchOutcome {
	p.mu.Lock()
	defer p.mu.Unlock()
	return fetchOutcome{snap: p.snapshot, at: p.snapshotAt, err: p.lastErr}
}

// doFetch runs one fetch and stores the outcome. On success the snapshot is
// replaced; on failure it is retained and the error state cached. The
// initial-loading flag clears when the first attempt completes either way;
// lastAttempt is recorded for the cooldown anchor.
func (p *plugin) doFetch(ctx context.Context, key string) fetchOutcome {
	snap, err := p.fetch.Fetch(ctx, key)
	res := fetchOutcome{snap: snap, at: p.now()}
	switch {
	case err != nil:
		res.err = balanceErrorOf(err)
	case snap == nil:
		res.err = &BalanceError{Code: codeBadResponse, Message: "DeepSeek balance client returned no data"}
	}

	p.mu.Lock()
	p.lastAttempt = res.at
	p.initialPollInFlight = false
	if res.err != nil {
		p.lastErr = res.err
	} else {
		p.snapshot = res.snap
		p.snapshotAt = res.at
		p.lastErr = nil
	}
	p.mu.Unlock()
	return res
}

func balanceErrorOf(err error) *BalanceError {
	if be, ok := err.(*BalanceError); ok {
		return be
	}
	return &BalanceError{Code: codeNetwork, Message: err.Error()}
}

// HandleAction serves the authenticated balance.get action. The workspace
// scope is host-verified (VerifiedActionContext.WorkspaceID); the bounded
// request body (the action envelope's `body`, JSON {"refresh": bool}) forces a
// rebuild when refresh is true, otherwise the current state is served.
// HTTP 200 is returned for every domain status — failures are body-encoded,
// never HTTP statuses (the host forwards plugin statuses verbatim and the
// UI's fetchJson throws on non-2xx).
func (p *plugin) HandleAction(ctx context.Context, req *pluginsdk.PluginActionRequest) (*pluginsdk.PluginActionResponse, error) {
	if req == nil || req.ActionKey != actionKeyBalanceGet {
		return jsonActionResponse(404, []byte(`{"error":"unknown plugin action"}`)), nil
	}
	refresh := parseRefresh(req.Body)
	return jsonActionResponse(200, p.balanceResponse(ctx, refresh)), nil
}

// balanceResponse builds the action body for the current state.
func (p *plugin) balanceResponse(ctx context.Context, refresh bool) []byte {
	p.mu.Lock()
	hostSet := p.hostSet
	initial := p.initialPollInFlight
	p.mu.Unlock()

	// Pre-SetHost window: config is not readable yet (the Host RPC is
	// unavailable), so the action is loading with warn_below null — never
	// unconfigured, never an error, never a panic.
	if !hostSet {
		return encodeActionResponse(statusLoading, nil, nil, nil, nil, nil)
	}

	cfg := p.readConfig(ctx)
	key := apiKey(cfg)
	if key == "" {
		return encodeActionResponse(statusUnconfigured, nil, nil, nil, nil, nil)
	}
	warn := positiveFloatOr(cfg[configKeyWarnBelow], defaultWarnBelow)

	// Initial poll pending or in flight: loading, non-blocking, even for a
	// forced refresh (the in-flight initial poll continues; the action does
	// NOT join it).
	if initial {
		return encodeActionResponse(statusLoading, &warn, nil, nil, nil, nil)
	}

	if refresh {
		// Cooldown is checked FIRST: within 5 s of the last completed fetch
		// attempt the action serves the current state immediately, even if
		// another poll is in flight. Otherwise the refresh joins an in-flight
		// poll instead of starting a second round trip.
		if p.inCooldown() {
			return p.currentStateResponse(&warn)
		}
		res := p.startFetch(ctx, key)
		return p.outcomeResponse(res, &warn)
	}
	return p.currentStateResponse(&warn)
}

// currentStateResponse serves the warm snapshot when one exists, otherwise
// the cached error state (e.g. a failed initial poll with balance_infos null).
func (p *plugin) currentStateResponse(warn *float64) []byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.lastErr != nil {
		// A failure never evicts the snapshot: carry the last-known balance
		// alongside the reason.
		if p.snapshot != nil {
			return encodeActionResponse(statusError, warn, p.lastErr, &p.snapshot.IsAvailable, p.snapshot.BalanceInfos, &p.snapshotAt)
		}
		return encodeActionResponse(statusError, warn, p.lastErr, nil, nil, nil)
	}
	if p.snapshot != nil {
		return encodeActionResponse(statusOK, warn, nil, &p.snapshot.IsAvailable, p.snapshot.BalanceInfos, &p.snapshotAt)
	}
	// Defensive: post-SetHost with a key and no completed attempt should be
	// inside the loading window; this keeps the classification honest.
	return encodeActionResponse(statusLoading, warn, nil, nil, nil, nil)
}

// outcomeResponse encodes a completed fetch (from a join or a fresh start).
func (p *plugin) outcomeResponse(res fetchOutcome, warn *float64) []byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	if res.err != nil {
		if p.snapshot != nil {
			return encodeActionResponse(statusError, warn, res.err, &p.snapshot.IsAvailable, p.snapshot.BalanceInfos, &p.snapshotAt)
		}
		return encodeActionResponse(statusError, warn, res.err, nil, nil, nil)
	}
	return encodeActionResponse(statusOK, warn, nil, &res.snap.IsAvailable, res.snap.BalanceInfos, &res.at)
}

func (p *plugin) inCooldown() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return !p.lastAttempt.IsZero() && p.now().Sub(p.lastAttempt) < cooldownWindow
}

// --- config -----------------------------------------------------------------

func (p *plugin) readConfig(ctx context.Context) map[string]any {
	host := p.Host()
	if host == nil {
		return map[string]any{}
	}
	cfg, err := host.GetConfig(ctx)
	if err != nil {
		log.Printf("kandev-deepseek-credits: reading plugin config: %v", err)
		return map[string]any{}
	}
	return cfg
}

// apiKeyFromConfig resolves the Bearer credential: the api_key settings
// secret wins, then DEEPSEEK_API_KEY. An empty or whitespace-only value
// (trimmed) counts as unset — no empty-Bearer fetch, no 401 noise. The key is
// TrimSpace'd ONCE, and the TRIMMED value is both the emptiness-check input
// and the credential: stray surrounding whitespace never produces a
// whitespace-padded Bearer that would 401-loop.
func (p *plugin) apiKeyFromConfig(ctx context.Context) string {
	return apiKey(p.readConfig(ctx))
}

func apiKey(cfg map[string]any) string {
	if v, ok := cfg[configKeyAPIKey].(string); ok {
		if k := strings.TrimSpace(v); k != "" {
			return k
		}
	}
	return strings.TrimSpace(os.Getenv(envAPIKey))
}

// pollInterval reads the background refresh interval from config (minutes).
// The manifest's `minimum: 1` is declarative only (neither the host's
// config-schema validation nor the settings form enforces it), so the runtime
// floor clamp here is the enforcement point: a present value below 1 (zero,
// negative, fractional) clamps to 1 minute. Missing means the 5-minute
// default.
func (p *plugin) pollInterval(ctx context.Context) time.Duration {
	minutes := floatConfig(p.readConfig(ctx)[configKeyPollMinutes], defaultPollMinutes)
	if minutes < minPollMinutes {
		minutes = minPollMinutes
	}
	return time.Duration(minutes * float64(time.Minute))
}

// floatConfig coerces a JSON config value (numbers arrive as float64), or
// returns the fallback when the key is absent or not a number.
func floatConfig(v any, fallback float64) float64 {
	if f, ok := v.(float64); ok {
		return f
	}
	return fallback
}

// positiveFloatOr coerces a JSON config value (numbers arrive as float64) to a
// positive float, or returns the fallback.
func positiveFloatOr(v any, fallback float64) float64 {
	f, ok := v.(float64)
	if !ok || f <= 0 {
		return fallback
	}
	return f
}

// --- response encoding --------------------------------------------------------

// parseRefresh reads the bounded action body. The body is attacker-controlled
// (any authenticated visible-workspace user can POST up to 1024 arbitrary
// bytes): unparseable JSON or a non-boolean refresh is treated as
// refresh:false — no panic, no non-200, no invented error code.
func parseRefresh(body []byte) bool {
	if len(bytes.TrimSpace(body)) == 0 {
		return false
	}
	var v struct {
		Refresh *bool `json:"refresh"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		return false
	}
	return v.Refresh != nil && *v.Refresh
}

type actionErrorJSON struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// actionResponseJSON is the exact action response contract. Pointers give
// JSON null for the unset fields.
type actionResponseJSON struct {
	Status       string           `json:"status"`
	Error        *actionErrorJSON `json:"error"`
	FetchedAt    *string          `json:"fetched_at"`
	IsAvailable  *bool            `json:"is_available"`
	BalanceInfos []BalanceInfo    `json:"balance_infos"`
	WarnBelow    *float64         `json:"warn_below"`
}

func encodeActionResponse(status string, warn *float64, err *BalanceError, isAvailable *bool, infos []BalanceInfo, fetchedAt *time.Time) []byte {
	var e *actionErrorJSON
	if err != nil {
		e = &actionErrorJSON{Code: err.Code, Message: err.Message}
	}
	var fa *string
	if fetchedAt != nil {
		s := fetchedAt.UTC().Format(time.RFC3339)
		fa = &s
	}
	out := actionResponseJSON{
		Status:       status,
		Error:        e,
		FetchedAt:    fa,
		IsAvailable:  isAvailable,
		BalanceInfos: infos,
		WarnBelow:    warn,
	}
	encoded, marshalErr := json.Marshal(out)
	if marshalErr != nil {
		// Cannot fail for this shape; never return a non-200.
		return []byte(`{"status":"error","error":{"code":"bad_response","message":"encoding balance response"}}`)
	}
	return encoded
}

func jsonActionResponse(status int, body []byte) *pluginsdk.PluginActionResponse {
	return &pluginsdk.PluginActionResponse{
		Status:  status,
		Body:    body,
		Headers: map[string]string{"Content-Type": "application/json"},
	}
}
