// Package main tests. Exercises the plugin's balance.get action and warm
// snapshot poller against a fake Host and an injected fake balance client —
// no go-plugin spawn, no network. Mirrors the task-04 acceptance matrix:
// status windows (ok/loading/unconfigured/error), cooldown, singleflight
// joins, config precedence and trimming, warn_below/poll_minutes parsing,
// malformed bodies, and pre-SetHost behavior.
package main

import (
	"context"
	"encoding/json"

	"sync"
	"testing"
	"time"

	"github.com/kandev/kandev/pkg/pluginsdk"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Test doubles
// ---------------------------------------------------------------------------

// fakeHost serves GetConfig — the only Host surface this plugin uses.
// UnimplementedHostData satisfies the data-accessor methods.
type fakeHost struct {
	pluginsdk.UnimplementedHostData
	config map[string]any
}

func (h *fakeHost) GetState(context.Context, string, string, string) (map[string]any, bool, error) {
	return nil, false, nil
}
func (h *fakeHost) SetState(context.Context, string, string, string, map[string]any) error {
	return nil
}
func (h *fakeHost) DeleteState(context.Context, string, string, string) error { return nil }
func (h *fakeHost) ListState(context.Context, string, string) ([]pluginsdk.StateEntry, error) {
	return nil, nil
}
func (h *fakeHost) GetConfig(context.Context) (map[string]any, error) {
	if h.config == nil {
		return map[string]any{}, nil
	}
	return h.config, nil
}
func (h *fakeHost) RevealSecret(context.Context, string) (string, error) { return "", nil }
func (h *fakeHost) GetSecret(context.Context, string) (string, bool, error) {
	return "", false, nil
}
func (h *fakeHost) SetSecret(context.Context, string, string) error         { return nil }
func (h *fakeHost) DeleteSecret(context.Context, string) error              { return nil }
func (h *fakeHost) EmitEvent(context.Context, string, map[string]any) error { return nil }

var _ pluginsdk.Host = (*fakeHost)(nil)

// fetchResult is one scripted fake-client outcome.
type fetchResult struct {
	snap *Balance
	err  error
}

// fakeFetcher is a scripted balanceFetcher: results are consumed in order per
// call; when block is non-nil, calls with index >= blockAfter block until the
// channel is closed (in-flight simulation). Calls and keys are recorded.
type fakeFetcher struct {
	mu         sync.Mutex
	calls      int
	keys       []string
	results    []fetchResult
	block      chan struct{}
	blockAfter int
}

func (f *fakeFetcher) Fetch(ctx context.Context, key string) (*Balance, error) {
	f.mu.Lock()
	n := f.calls
	f.calls++
	f.keys = append(f.keys, key)
	var r fetchResult
	if len(f.results) > 0 {
		r = f.results[0]
		f.results = f.results[1:]
	}
	block, blockAfter := f.block, f.blockAfter
	f.mu.Unlock()

	if block != nil && n >= blockAfter {
		select {
		case <-block:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return r.snap, r.err
}

func (f *fakeFetcher) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *fakeFetcher) keysSeen() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.keys))
	copy(out, f.keys)
	return out
}

// testClock drives the plugin's now() seam deterministically.
type testClock struct {
	t time.Time
}

func (c *testClock) now() time.Time { return c.t }
func (c *testClock) advance(d time.Duration) {
	c.t = c.t.Add(d)
}

// newTestPlugin wires a plugin with a scripted fetcher, a controllable clock,
// and an optional fake Host config. disablePoller skips the background
// goroutine so tests drive pollOnce manually.
func newTestPlugin(t *testing.T, cfg map[string]any, f *fakeFetcher, disablePoller bool) (*plugin, *testClock) {
	t.Helper()
	p := newPlugin()
	p.fetch = f
	p.disablePoller = disablePoller
	clock := &testClock{t: time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)}
	p.now = clock.now
	p.SetHost(&fakeHost{config: cfg})
	return p, clock
}

func actionReq(body string) *pluginsdk.PluginActionRequest {
	return &pluginsdk.PluginActionRequest{
		ActionKey: "balance.get",
		Context:   pluginsdk.VerifiedActionContext{WorkspaceID: "ws-1"},
		Body:      []byte(body),
	}
}

func unknownKeyReq() *pluginsdk.PluginActionRequest {
	return &pluginsdk.PluginActionRequest{ActionKey: "other.action", Body: []byte(`{}`)}
}

// decodeResponse unmarshals a 200 action response into a map for assertions.
func decodeResponse(t *testing.T, resp *pluginsdk.PluginActionResponse) map[string]any {
	t.Helper()
	require.Equal(t, 200, resp.Status)
	var v map[string]any
	require.NoError(t, json.Unmarshal(resp.Body, &v))
	return v
}

func str(v map[string]any, key string) string {
	s, _ := v[key].(string)
	return s
}

func field(v map[string]any, key string) any { return v[key] }

// waitFor polls fn until it returns true or times out.
func waitFor(t *testing.T, what string, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

var sampleInfos = []BalanceInfo{
	{Currency: "CNY", TotalBalance: "110.00", GrantedBalance: "10.00", ToppedUpBalance: "100.00"},
}

func bal(available bool, infos []BalanceInfo) *Balance {
	return &Balance{IsAvailable: available, BalanceInfos: infos}
}

var errInvalidKey = &BalanceError{Code: codeInvalidKey, Message: "DeepSeek rejected the API key (401)"}

// ---------------------------------------------------------------------------
// Status windows
// ---------------------------------------------------------------------------

func TestHandleAction_BeforeSetHost_IsLoadingWithNullWarn(t *testing.T) {
	p := newPlugin() // never SetHost'ed
	f := &fakeFetcher{}
	p.fetch = f
	p.now = func() time.Time { return time.Now() }

	resp, err := p.HandleAction(context.Background(), actionReq(`{}`))
	require.NoError(t, err)
	v := decodeResponse(t, resp)
	require.Equal(t, "loading", str(v, "status"))
	require.Nil(t, field(v, "warn_below"))
	require.Nil(t, field(v, "fetched_at"))
	require.Nil(t, field(v, "balance_infos"))
	require.Nil(t, field(v, "error"))
	require.Equal(t, 0, f.callCount(), "no fetch may start before SetHost")
}

func TestHandleAction_Unconfigured_ServesUnconfiguredWithZeroFetches(t *testing.T) {
	f := &fakeFetcher{}
	p, _ := newTestPlugin(t, map[string]any{}, f, false)

	resp, err := p.HandleAction(context.Background(), actionReq(`{}`))
	require.NoError(t, err)
	v := decodeResponse(t, resp)
	require.Equal(t, "unconfigured", str(v, "status"))
	require.Nil(t, field(v, "warn_below"))
	require.Nil(t, field(v, "fetched_at"))
	require.Nil(t, field(v, "is_available"))
	require.Nil(t, field(v, "balance_infos"))
	require.Nil(t, field(v, "error"))
	require.Equal(t, 0, f.callCount(), "unconfigured poller must make zero DeepSeek round trips")
}

func TestHandleAction_LoadingWindow_FromSetHostThroughInitialPoll(t *testing.T) {
	f := &fakeFetcher{results: []fetchResult{{snap: bal(true, sampleInfos)}}, block: make(chan struct{}), blockAfter: 0}
	p, _ := newTestPlugin(t, map[string]any{"api_key": "sk-abc"}, f, false)

	// Immediately after SetHost, before the goroutine has done anything: loading
	// (the pending flag is set synchronously in SetHost — no gap window).
	resp, err := p.HandleAction(context.Background(), actionReq(`{}`))
	require.NoError(t, err)
	v := decodeResponse(t, resp)
	require.Equal(t, "loading", str(v, "status"))
	require.Equal(t, 10.0, field(v, "warn_below"), "post-SetHost loading carries the effective warn_below")
	require.Nil(t, field(v, "balance_infos"))
	require.Nil(t, field(v, "error"))

	// Wait until the initial fetch is actually in flight (blocked), then prove
	// a refresh:true during it returns loading immediately with ZERO extra
	// round trips.
	waitFor(t, "initial fetch in flight", func() bool { return f.callCount() == 1 })
	resp, err = p.HandleAction(context.Background(), actionReq(`{"refresh": true}`))
	require.NoError(t, err)
	v = decodeResponse(t, resp)
	require.Equal(t, "loading", str(v, "status"))
	require.Equal(t, 1, f.callCount(), "refresh during initial poll must not start another fetch")

	// Release the initial poll: the action settles to ok.
	close(f.block)
	waitFor(t, "initial poll to complete", func() bool {
		r, _ := p.HandleAction(context.Background(), actionReq(`{}`))
		return str(decodeResponse(t, r), "status") == "ok"
	})
	require.Equal(t, 1, f.callCount())
}

func TestHandleAction_FailedInitialPoll_IsErrorWithNullBalanceInfos(t *testing.T) {
	f := &fakeFetcher{results: []fetchResult{{err: errInvalidKey}}}
	p, _ := newTestPlugin(t, map[string]any{"api_key": "sk-bad"}, f, false)

	waitFor(t, "initial poll to fail", func() bool {
		r, _ := p.HandleAction(context.Background(), actionReq(`{}`))
		return str(decodeResponse(t, r), "status") == "error"
	})

	resp, _ := p.HandleAction(context.Background(), actionReq(`{}`))
	v := decodeResponse(t, resp)
	require.Equal(t, "error", str(v, "status"))
	require.Equal(t, "invalid_key", str(field(v, "error").(map[string]any), "code"))
	require.Nil(t, field(v, "balance_infos"))
	require.Nil(t, field(v, "is_available"))
	require.Nil(t, field(v, "fetched_at"))
	require.Equal(t, 10.0, field(v, "warn_below"))
}

func TestHandleAction_PollFailureAfterSuccess_RetainsSnapshot(t *testing.T) {
	f := &fakeFetcher{results: []fetchResult{{snap: bal(true, sampleInfos)}, {err: errInvalidKey}}}
	p, _ := newTestPlugin(t, map[string]any{"api_key": "sk-abc"}, f, false)

	waitFor(t, "initial success", func() bool {
		r, _ := p.HandleAction(context.Background(), actionReq(`{}`))
		return str(decodeResponse(t, r), "status") == "ok"
	})

	// Second poll (periodic) fails.
	done := make(chan struct{})
	go func() { p.pollOnce(context.Background()); close(done) }()
	<-done

	resp, _ := p.HandleAction(context.Background(), actionReq(`{}`))
	v := decodeResponse(t, resp)
	require.Equal(t, "error", str(v, "status"), "a failed poll surfaces the error state")
	require.Equal(t, "invalid_key", str(field(v, "error").(map[string]any), "code"))
	// The last-known snapshot is retained and re-rendered.
	require.Equal(t, "110.00", str(field(v, "balance_infos").([]any)[0].(map[string]any), "total_balance"))
	require.Equal(t, true, field(v, "is_available"))
	require.NotNil(t, field(v, "fetched_at"))
}

// ---------------------------------------------------------------------------
// Config precedence and trimming
// ---------------------------------------------------------------------------

func TestConfigPrecedence_SecretWinsOverEnv(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "env-key")
	f := &fakeFetcher{results: []fetchResult{{snap: bal(true, sampleInfos)}}}
	p, _ := newTestPlugin(t, map[string]any{"api_key": "secret-key"}, f, true)

	p.startFetch(context.Background(), p.apiKeyFromConfig(context.Background()))
	require.Equal(t, []string{"secret-key"}, f.keysSeen())
}

func TestConfigPrecedence_EmptyOrWhitespaceSecretFallsThroughToEnv(t *testing.T) {
	for _, stored := range []string{"", "   "} {
		t.Run("stored="+jsonStr(stored), func(t *testing.T) {
			t.Setenv("DEEPSEEK_API_KEY", "env-key")
			f := &fakeFetcher{results: []fetchResult{{snap: bal(true, sampleInfos)}}}
			p, _ := newTestPlugin(t, map[string]any{"api_key": stored}, f, true)

			p.startFetch(context.Background(), p.apiKeyFromConfig(context.Background()))
			require.Equal(t, []string{"env-key"}, f.keysSeen())
		})
	}
}

func TestConfigPrecedence_NoKeyAnywhere_UnconfiguredZeroFetches(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "")
	f := &fakeFetcher{}
	p, _ := newTestPlugin(t, map[string]any{"api_key": "   "}, f, false)

	resp, _ := p.HandleAction(context.Background(), actionReq(`{}`))
	require.Equal(t, "unconfigured", str(decodeResponse(t, resp), "status"))
	require.Equal(t, 0, f.callCount(), "no empty-Bearer fetch may ever happen")
}

func TestConfigPrecedence_WhitespaceOnlyEnvCountsAsUnset(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "   ")
	f := &fakeFetcher{}
	p, _ := newTestPlugin(t, map[string]any{}, f, false)

	resp, _ := p.HandleAction(context.Background(), actionReq(`{}`))
	require.Equal(t, "unconfigured", str(decodeResponse(t, resp), "status"))
	require.Equal(t, 0, f.callCount())
}

func TestConfigPrecedence_StoredKeyTrimmedForCredential(t *testing.T) {
	f := &fakeFetcher{results: []fetchResult{{snap: bal(true, sampleInfos)}}}
	p, _ := newTestPlugin(t, map[string]any{"api_key": " sk-abc "}, f, true)

	p.startFetch(context.Background(), p.apiKeyFromConfig(context.Background()))
	require.Equal(t, []string{"sk-abc"}, f.keysSeen(), "Bearer must use the TRIMMED key")
}

func TestConfigPrecedence_EnvKeyTrimmedForCredential(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", " sk-abc ")
	f := &fakeFetcher{results: []fetchResult{{snap: bal(true, sampleInfos)}}}
	p, _ := newTestPlugin(t, map[string]any{}, f, true)

	p.startFetch(context.Background(), p.apiKeyFromConfig(context.Background()))
	require.Equal(t, []string{"sk-abc"}, f.keysSeen())
}

// ---------------------------------------------------------------------------
// refresh semantics: rebuild, cooldown, joins
// ---------------------------------------------------------------------------

func TestRefresh_ForcesRebuildWhilePlainCallServesCache(t *testing.T) {
	f := &fakeFetcher{results: []fetchResult{
		{snap: bal(true, []BalanceInfo{{Currency: "CNY", TotalBalance: "10.00", GrantedBalance: "0.00", ToppedUpBalance: "10.00"}})},
		{snap: bal(true, []BalanceInfo{{Currency: "CNY", TotalBalance: "99.00", GrantedBalance: "0.00", ToppedUpBalance: "99.00"}})},
	}}
	p, clock := newTestPlugin(t, map[string]any{"api_key": "sk-abc"}, f, false)
	waitFor(t, "initial success", func() bool {
		r, _ := p.HandleAction(context.Background(), actionReq(`{}`))
		return str(decodeResponse(t, r), "status") == "ok"
	})
	clock.advance(6 * time.Second) // leave the 5s cooldown

	// Plain call: serves the cache, no new round trip.
	resp, _ := p.HandleAction(context.Background(), actionReq(`{}`))
	v := decodeResponse(t, resp)
	require.Equal(t, "10.00", str(field(v, "balance_infos").([]any)[0].(map[string]any), "total_balance"))
	require.Equal(t, 1, f.callCount())

	// refresh:true: forced rebuild.
	resp, _ = p.HandleAction(context.Background(), actionReq(`{"refresh": true}`))
	v = decodeResponse(t, resp)
	require.Equal(t, "ok", str(v, "status"))
	require.Equal(t, "99.00", str(field(v, "balance_infos").([]any)[0].(map[string]any), "total_balance"))
	require.Equal(t, 2, f.callCount())
}

func TestRefresh_CooldownServesCurrentStateWithZeroRoundTrips(t *testing.T) {
	f := &fakeFetcher{results: []fetchResult{
		{snap: bal(true, []BalanceInfo{{Currency: "CNY", TotalBalance: "10.00", GrantedBalance: "0.00", ToppedUpBalance: "10.00"}})},
		{snap: bal(true, []BalanceInfo{{Currency: "CNY", TotalBalance: "99.00", GrantedBalance: "0.00", ToppedUpBalance: "99.00"}})},
	}}
	p, clock := newTestPlugin(t, map[string]any{"api_key": "sk-abc"}, f, false)
	waitFor(t, "initial success", func() bool {
		r, _ := p.HandleAction(context.Background(), actionReq(`{}`))
		return str(decodeResponse(t, r), "status") == "ok"
	})

	// 2s after the completed attempt: cooldown serves the current state.
	clock.advance(2 * time.Second)
	resp, _ := p.HandleAction(context.Background(), actionReq(`{"refresh": true}`))
	v := decodeResponse(t, resp)
	require.Equal(t, "ok", str(v, "status"))
	require.Equal(t, "10.00", str(field(v, "balance_infos").([]any)[0].(map[string]any), "total_balance"))
	require.Equal(t, 1, f.callCount(), "cooldown must serve without a new round trip")

	// 6s after the attempt: cooldown expired, refresh fetches.
	clock.advance(4 * time.Second)
	resp, _ = p.HandleAction(context.Background(), actionReq(`{"refresh": true}`))
	v = decodeResponse(t, resp)
	require.Equal(t, "99.00", str(field(v, "balance_infos").([]any)[0].(map[string]any), "total_balance"))
	require.Equal(t, 2, f.callCount())
}

func TestRefresh_CooldownAfterFailedAttempt_ServesCachedErrorState(t *testing.T) {
	f := &fakeFetcher{results: []fetchResult{{err: errInvalidKey}}}
	p, clock := newTestPlugin(t, map[string]any{"api_key": "sk-bad"}, f, false)
	waitFor(t, "initial failure", func() bool {
		r, _ := p.HandleAction(context.Background(), actionReq(`{}`))
		return str(decodeResponse(t, r), "status") == "error"
	})

	// Cooldown anchors on ATTEMPTS, not successes: a refresh inside 5s of the
	// failed attempt serves the cached error state with zero round trips.
	clock.advance(2 * time.Second)
	resp, _ := p.HandleAction(context.Background(), actionReq(`{"refresh": true}`))
	v := decodeResponse(t, resp)
	require.Equal(t, "error", str(v, "status"))
	require.Equal(t, "invalid_key", str(field(v, "error").(map[string]any), "code"))
	require.Nil(t, field(v, "balance_infos"))
	require.Equal(t, 1, f.callCount())

	// After the cooldown expires, refresh retries.
	clock.advance(4 * time.Second)
	resp, _ = p.HandleAction(context.Background(), actionReq(`{"refresh": true}`))
	v = decodeResponse(t, resp)
	require.Equal(t, "error", str(v, "status"))
	require.Equal(t, 2, f.callCount())
}

func TestRefresh_JoinsInFlightPoll(t *testing.T) {
	f := &fakeFetcher{
		results: []fetchResult{
			{snap: bal(true, []BalanceInfo{{Currency: "CNY", TotalBalance: "10.00", GrantedBalance: "0.00", ToppedUpBalance: "10.00"}})},
			{snap: bal(true, []BalanceInfo{{Currency: "CNY", TotalBalance: "55.00", GrantedBalance: "0.00", ToppedUpBalance: "55.00"}})},
		},
		block:      make(chan struct{}),
		blockAfter: 1, // first call (initial) completes; second (poll) blocks
	}
	p, clock := newTestPlugin(t, map[string]any{"api_key": "sk-abc"}, f, false)
	waitFor(t, "initial success", func() bool {
		r, _ := p.HandleAction(context.Background(), actionReq(`{}`))
		return str(decodeResponse(t, r), "status") == "ok"
	})
	clock.advance(6 * time.Second)

	// Start a poll that blocks in flight.
	go p.pollOnce(context.Background())
	waitFor(t, "poll in flight", func() bool { return f.callCount() == 2 })

	// refresh:true JOINS the in-flight poll (waits for it), no second fetch.
	respCh := make(chan *pluginsdk.PluginActionResponse)
	go func() {
		r, _ := p.HandleAction(context.Background(), actionReq(`{"refresh": true}`))
		respCh <- r
	}()
	time.Sleep(50 * time.Millisecond)
	require.Equal(t, 2, f.callCount(), "refresh must join, not start a second fetch")

	close(f.block)
	resp := <-respCh
	v := decodeResponse(t, resp)
	require.Equal(t, "ok", str(v, "status"))
	require.Equal(t, "55.00", str(field(v, "balance_infos").([]any)[0].(map[string]any), "total_balance"),
		"a joined refresh returns the joined fetch's outcome (fresh data)")
	require.Equal(t, 2, f.callCount())
}

func TestRefresh_JoinsPostFailureRetry_ReturnsItsOutcome(t *testing.T) {
	t.Run("retry recovers", func(t *testing.T) {
		f := &fakeFetcher{
			results: []fetchResult{
				{err: errInvalidKey},
				{snap: bal(true, sampleInfos)},
			},
			block:      make(chan struct{}),
			blockAfter: 1,
		}
		p, clock := newTestPlugin(t, map[string]any{"api_key": "sk-abc"}, f, false)
		waitFor(t, "initial failure", func() bool {
			r, _ := p.HandleAction(context.Background(), actionReq(`{}`))
			return str(decodeResponse(t, r), "status") == "error"
		})
		clock.advance(6 * time.Second)

		go p.pollOnce(context.Background()) // periodic retry, blocked
		waitFor(t, "retry in flight", func() bool { return f.callCount() == 2 })

		respCh := make(chan *pluginsdk.PluginActionResponse)
		go func() {
			r, _ := p.HandleAction(context.Background(), actionReq(`{"refresh": true}`))
			respCh <- r
		}()
		time.Sleep(50 * time.Millisecond)
		require.Equal(t, 2, f.callCount(), "refresh must join the retry, zero extra round trips")

		close(f.block)
		v := decodeResponse(t, <-respCh)
		require.Equal(t, "ok", str(v, "status"), "joined retry recovers to ok")
		require.Equal(t, 2, f.callCount())
	})

	t.Run("retry fails", func(t *testing.T) {
		f := &fakeFetcher{
			results: []fetchResult{
				{err: errInvalidKey},
				{err: &BalanceError{Code: codeRateLimited, Message: "DeepSeek rate limited the request (429)"}},
			},
			block:      make(chan struct{}),
			blockAfter: 1,
		}
		p, clock := newTestPlugin(t, map[string]any{"api_key": "sk-abc"}, f, false)
		waitFor(t, "initial failure", func() bool {
			r, _ := p.HandleAction(context.Background(), actionReq(`{}`))
			return str(decodeResponse(t, r), "status") == "error"
		})
		clock.advance(6 * time.Second)

		go p.pollOnce(context.Background())
		waitFor(t, "retry in flight", func() bool { return f.callCount() == 2 })

		respCh := make(chan *pluginsdk.PluginActionResponse)
		go func() {
			r, _ := p.HandleAction(context.Background(), actionReq(`{"refresh": true}`))
			respCh <- r
		}()
		time.Sleep(50 * time.Millisecond)
		close(f.block)

		v := decodeResponse(t, <-respCh)
		require.Equal(t, "error", str(v, "status"), "joined retry failure is error, NEVER loading")
		require.Equal(t, "rate_limited", str(field(v, "error").(map[string]any), "code"))
		require.Equal(t, 2, f.callCount())
	})
}

func TestRefresh_CooldownWinsOverJoin(t *testing.T) {
	f := &fakeFetcher{
		results: []fetchResult{
			{snap: bal(true, []BalanceInfo{{Currency: "CNY", TotalBalance: "10.00", GrantedBalance: "0.00", ToppedUpBalance: "10.00"}})},
			{snap: bal(true, []BalanceInfo{{Currency: "CNY", TotalBalance: "55.00", GrantedBalance: "0.00", ToppedUpBalance: "55.00"}})},
		},
		block:      make(chan struct{}),
		blockAfter: 1,
	}
	p, clock := newTestPlugin(t, map[string]any{"api_key": "sk-abc"}, f, false)
	waitFor(t, "initial success", func() bool {
		r, _ := p.HandleAction(context.Background(), actionReq(`{}`))
		return str(decodeResponse(t, r), "status") == "ok"
	})

	go p.pollOnce(context.Background()) // poll in flight, blocked
	waitFor(t, "poll in flight", func() bool { return f.callCount() == 2 })

	// Inside the 5s cooldown while a poll is in flight: the cooldown wins —
	// serve the current state immediately, no join, no round trip.
	clock.advance(2 * time.Second)
	resp, err := p.HandleAction(context.Background(), actionReq(`{"refresh": true}`))
	require.NoError(t, err)
	v := decodeResponse(t, resp)
	require.Equal(t, "ok", str(v, "status"))
	require.Equal(t, "10.00", str(field(v, "balance_infos").([]any)[0].(map[string]any), "total_balance"))
	require.Equal(t, 2, f.callCount(), "cooldown serves without joining or fetching")

	// The in-flight poll is still blocked (the action did not wait on it).
	require.Equal(t, 2, f.callCount())
	close(f.block)
}

// ---------------------------------------------------------------------------
// warn_below and poll_minutes
// ---------------------------------------------------------------------------

func TestWarnBelow_DefaultsAndParsing(t *testing.T) {
	cases := []struct {
		name string
		cfg  map[string]any
		want float64
	}{
		{"unset defaults to 10", map[string]any{"api_key": "sk-abc"}, 10},
		{"zero means default", map[string]any{"api_key": "sk-abc", "warn_below": 0.0}, 10},
		{"negative means default", map[string]any{"api_key": "sk-abc", "warn_below": -3.0}, 10},
		{"configured value wins", map[string]any{"api_key": "sk-abc", "warn_below": 5.0}, 5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeFetcher{results: []fetchResult{{snap: bal(true, sampleInfos)}}}
			p, _ := newTestPlugin(t, tc.cfg, f, false)
			waitFor(t, "initial success", func() bool {
				r, _ := p.HandleAction(context.Background(), actionReq(`{}`))
				return str(decodeResponse(t, r), "status") == "ok"
			})
			resp, _ := p.HandleAction(context.Background(), actionReq(`{}`))
			require.Equal(t, tc.want, field(decodeResponse(t, resp), "warn_below"))
		})
	}
}

func TestDisplayOptionsDefaultAndConfigured(t *testing.T) {
	cases := []struct {
		name   string
		cfg    map[string]any
		top    bool
		prompt bool
	}{
		{"defaults", map[string]any{}, true, false},
		{"configured", map[string]any{
			configKeyDisplayTaskTopRight: false,
			configKeyDisplayPromptInput:  true,
		}, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, _ := newTestPlugin(t, tc.cfg, &fakeFetcher{}, true)
			resp, err := p.HandleAction(context.Background(), actionReq(`{}`))
			require.NoError(t, err)
			v := decodeResponse(t, resp)
			require.Equal(t, tc.top, field(v, configKeyDisplayTaskTopRight))
			require.Equal(t, tc.prompt, field(v, configKeyDisplayPromptInput))
		})
	}
}

func TestPollMinutes_Floor(t *testing.T) {
	cfg := func(v any) map[string]any { return map[string]any{"api_key": "sk-abc", "poll_minutes": v} }
	cases := []struct {
		name string
		cfg  map[string]any
		want time.Duration
	}{
		{"default 5", map[string]any{"api_key": "sk-abc"}, 5 * time.Minute},
		{"configured 7", cfg(7.0), 7 * time.Minute},
		{"floor at 1", cfg(0.5), time.Minute},
		{"floor at 1 for zero", cfg(0.0), time.Minute},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeFetcher{}
			p, _ := newTestPlugin(t, tc.cfg, f, true)
			require.Equal(t, tc.want, p.pollInterval(context.Background()))
		})
	}
}

// ---------------------------------------------------------------------------
// Response shape details
// ---------------------------------------------------------------------------

func TestResponse_IsAvailableBooleanAndFetchedAt(t *testing.T) {
	f := &fakeFetcher{results: []fetchResult{{snap: bal(false, sampleInfos)}}}
	p, _ := newTestPlugin(t, map[string]any{"api_key": "sk-abc"}, f, false)
	waitFor(t, "initial success", func() bool {
		r, _ := p.HandleAction(context.Background(), actionReq(`{}`))
		return str(decodeResponse(t, r), "status") == "ok"
	})
	resp, _ := p.HandleAction(context.Background(), actionReq(`{}`))
	v := decodeResponse(t, resp)
	require.Equal(t, false, field(v, "is_available"))
	fetched, ok := field(v, "fetched_at").(string)
	require.True(t, ok)
	_, err := time.Parse(time.RFC3339, fetched)
	require.NoError(t, err, "fetched_at must be RFC 3339")
}

func TestHandleAction_UnknownActionKeyRejected(t *testing.T) {
	f := &fakeFetcher{}
	p, _ := newTestPlugin(t, map[string]any{}, f, true)
	resp, err := p.HandleAction(context.Background(), unknownKeyReq())
	require.NoError(t, err)
	require.NotEqual(t, 200, resp.Status, "unknown action keys are rejected, not served")
}

func TestHandleAction_MalformedBodiesTreatedAsRefreshFalse(t *testing.T) {
	f := &fakeFetcher{results: []fetchResult{{snap: bal(true, sampleInfos)}}}
	p, clock := newTestPlugin(t, map[string]any{"api_key": "sk-abc"}, f, false)
	waitFor(t, "initial success", func() bool {
		r, _ := p.HandleAction(context.Background(), actionReq(`{}`))
		return str(decodeResponse(t, r), "status") == "ok"
	})
	clock.advance(6 * time.Second)

	for _, body := range []string{`{"refresh":"yes"}`, `garbage`, `{"refresh": [1]}`, ``} {
		resp, err := p.HandleAction(context.Background(), actionReq(body))
		require.NoError(t, err, "body %q must not panic", body)
		v := decodeResponse(t, resp)
		require.Equal(t, "ok", str(v, "status"), "body %q served as refresh:false", body)
	}
	require.Equal(t, 1, f.callCount(), "malformed bodies must not trigger fetches")
}

func TestHandleAction_RefreshTrueWithExtraKeysStillRefreshes(t *testing.T) {
	f := &fakeFetcher{results: []fetchResult{
		{snap: bal(true, []BalanceInfo{{Currency: "CNY", TotalBalance: "10.00", GrantedBalance: "0.00", ToppedUpBalance: "10.00"}})},
		{snap: bal(true, []BalanceInfo{{Currency: "CNY", TotalBalance: "99.00", GrantedBalance: "0.00", ToppedUpBalance: "99.00"}})},
	}}
	p, clock := newTestPlugin(t, map[string]any{"api_key": "sk-abc"}, f, false)
	waitFor(t, "initial success", func() bool {
		r, _ := p.HandleAction(context.Background(), actionReq(`{}`))
		return str(decodeResponse(t, r), "status") == "ok"
	})
	clock.advance(6 * time.Second)

	// A JSON object that IS the expected shape (boolean refresh) with extra
	// ignored keys is a real refresh.
	resp, err := p.HandleAction(context.Background(), actionReq(`{"refresh": true, "extra": 1}`))
	require.NoError(t, err)
	v := decodeResponse(t, resp)
	require.Equal(t, "ok", str(v, "status"))
	require.Equal(t, "99.00", str(field(v, "balance_infos").([]any)[0].(map[string]any), "total_balance"))
	require.Equal(t, 2, f.callCount())
}

func TestHandleAction_PlainCallDuringInFlightRetryServesCurrentState(t *testing.T) {
	f := &fakeFetcher{
		results: []fetchResult{
			{snap: bal(true, sampleInfos)},
			{snap: bal(true, sampleInfos)},
		},
		block:      make(chan struct{}),
		blockAfter: 1,
	}
	p, _ := newTestPlugin(t, map[string]any{"api_key": "sk-abc"}, f, false)
	waitFor(t, "initial success", func() bool {
		r, _ := p.HandleAction(context.Background(), actionReq(`{}`))
		return str(decodeResponse(t, r), "status") == "ok"
	})

	go p.pollOnce(context.Background())
	waitFor(t, "poll in flight", func() bool { return f.callCount() == 2 })

	// A plain (non-refresh) call while the poll is in flight serves the
	// current state without joining or waiting.
	resp, _ := p.HandleAction(context.Background(), actionReq(`{}`))
	v := decodeResponse(t, resp)
	require.Equal(t, "ok", str(v, "status"))
	require.Equal(t, 2, f.callCount())
	close(f.block)
}

// jsonStr renders a string for test names.
func jsonStr(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
