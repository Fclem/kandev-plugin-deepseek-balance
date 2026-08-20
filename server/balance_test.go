// Package main tests. Exercises the DeepSeek balance client against httptest
// servers — no network, no plugin wiring. Mirrors the task-03 acceptance
// matrix: golden payload, the full error taxonomy, shape validation, the 1 MiB
// cap, the 10 s timeout, Bearer header assertion, and error redaction.
package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// testAPIKey is the key every redaction assertion checks is absent from error
// strings.
const testAPIKey = "sk-test-secret-key"

// withBalanceBase runs fn with the client base URL pointed at srv and restores
// the previous value afterwards.
func withBalanceBase(t *testing.T, srv *httptest.Server, fn func()) {
	t.Helper()
	old := balanceAPIBase
	balanceAPIBase = srv.URL
	t.Cleanup(func() { balanceAPIBase = old })
	fn()
}

// withBalanceTimeout runs fn with the client timeout set to d.
func withBalanceTimeout(t *testing.T, d time.Duration, fn func()) {
	t.Helper()
	old := balanceTimeout
	balanceTimeout = d
	t.Cleanup(func() { balanceTimeout = old })
	fn()
}

func TestFetchBalance_GoldenPayload(t *testing.T) {
	var gotAuth atomic.Value
	var gotPath atomic.Value
	var gotMethod atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth.Store(r.Header.Get("Authorization"))
		gotPath.Store(r.URL.Path)
		gotMethod.Store(r.Method)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"is_available": true, "balance_infos": [{"currency": "CNY", "total_balance": "110.00", "granted_balance": "10.00", "topped_up_balance": "100.00"}]}`)
	}))
	defer srv.Close()

	withBalanceBase(t, srv, func() {
		b, err := fetchBalance(context.Background(), testAPIKey)
		require.NoError(t, err)
		require.Equal(t, http.MethodGet, gotMethod.Load().(string))
		require.Equal(t, "/user/balance", gotPath.Load().(string))
		require.Equal(t, "Bearer "+testAPIKey, gotAuth.Load().(string))
		require.True(t, b.IsAvailable)
		require.Len(t, b.BalanceInfos, 1)
		require.Equal(t, BalanceInfo{Currency: "CNY", TotalBalance: "110.00", GrantedBalance: "10.00", ToppedUpBalance: "100.00"}, b.BalanceInfos[0])
	})
}

func TestFetchBalance_PreservesBalanceInfosOrder(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"is_available": true, "balance_infos": [{"currency": "CNY", "total_balance": "1.00", "granted_balance": "0.00", "topped_up_balance": "1.00"}, {"currency": "USD", "total_balance": "2.00", "granted_balance": "0.00", "topped_up_balance": "2.00"}]}`)
	}))
	defer srv.Close()

	withBalanceBase(t, srv, func() {
		b, err := fetchBalance(context.Background(), testAPIKey)
		require.NoError(t, err)
		require.Len(t, b.BalanceInfos, 2)
		require.Equal(t, "CNY", b.BalanceInfos[0].Currency)
		require.Equal(t, "USD", b.BalanceInfos[1].Currency)
	})
}

func TestFetchBalance_ErrorTaxonomy(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		body     string
		wantCode string
	}{
		{"401 invalid key", http.StatusUnauthorized, `{"error": {"message": "Invalid Authentication"}}`, codeInvalidKey},
		{"402 insufficient balance", http.StatusPaymentRequired, `{"error": {"message": "Insufficient Balance"}}`, codeInsufficientBalance},
		{"429 rate limited", http.StatusTooManyRequests, `{"error": {"message": "rate limit"}}`, codeRateLimited},
		{"500 upstream failure", http.StatusInternalServerError, `{}`, codeHTTP},
		{"502 bad gateway", http.StatusBadGateway, `{}`, codeHTTP},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.body)
			}))
			defer srv.Close()

			withBalanceBase(t, srv, func() {
				b, err := fetchBalance(context.Background(), testAPIKey)
				require.Nil(t, b)
				require.Error(t, err)
				require.Equal(t, tc.wantCode, errCode(err), "expected code %s, got %v", tc.wantCode, err)
				assertSecretFree(t, err)
			})
		})
	}
}

func TestFetchBalance_MalformedBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `this is not json`)
	}))
	defer srv.Close()

	withBalanceBase(t, srv, func() {
		b, err := fetchBalance(context.Background(), testAPIKey)
		require.Nil(t, b)
		require.Equal(t, codeBadResponse, errCode(err))
		assertSecretFree(t, err)
	})
}

func TestFetchBalance_WrongShape(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"missing is_available", `{"balance_infos": []}`},
		{"currency outside enum", `{"is_available": true, "balance_infos": [{"currency": "EUR", "total_balance": "1.00", "granted_balance": "0.00", "topped_up_balance": "1.00"}]}`},
		{"non-numeric total_balance", `{"is_available": true, "balance_infos": [{"currency": "CNY", "total_balance": "abc", "granted_balance": "0.00", "topped_up_balance": "1.00"}]}`},
		{"non-numeric granted_balance", `{"is_available": true, "balance_infos": [{"currency": "CNY", "total_balance": "1.00", "granted_balance": "NaN", "topped_up_balance": "1.00"}]}`},
		{"non-numeric topped_up_balance", `{"is_available": true, "balance_infos": [{"currency": "CNY", "total_balance": "1.00", "granted_balance": "0.00", "topped_up_balance": "Inf"}]}`},
		{"missing balance_infos", `{"is_available": true}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				io.WriteString(w, tc.body)
			}))
			defer srv.Close()

			withBalanceBase(t, srv, func() {
				b, err := fetchBalance(context.Background(), testAPIKey)
				require.Nil(t, b)
				require.Equal(t, codeBadResponse, errCode(err))
				assertSecretFree(t, err)
			})
		})
	}
}

func TestFetchBalance_EmptyBalanceInfosIsValid(t *testing.T) {
	for _, available := range []bool{true, false} {
		t.Run(map[bool]string{true: "available", false: "unavailable"}[available], func(t *testing.T) {
			body := `{"is_available": ` + map[bool]string{true: "true", false: "false"}[available] + `, "balance_infos": []}`
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				io.WriteString(w, body)
			}))
			defer srv.Close()

			withBalanceBase(t, srv, func() {
				b, err := fetchBalance(context.Background(), testAPIKey)
				require.NoError(t, err)
				require.Equal(t, available, b.IsAvailable)
				require.Empty(t, b.BalanceInfos)
			})
		})
	}
}

func TestFetchBalance_OversizedBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.CopyN(w, strings.NewReader(`{"is_available": true, "balance_infos": [`), 40)
		io.CopyN(w, zeroReader{}, maxResponseBytes) // push past the cap
		io.WriteString(w, `]}`)
	}))
	defer srv.Close()

	withBalanceBase(t, srv, func() {
		b, err := fetchBalance(context.Background(), testAPIKey)
		require.Nil(t, b)
		require.Equal(t, codeBadResponse, errCode(err))
		assertSecretFree(t, err)
	})
}

// zeroReader yields an endless stream of ' ' bytes.
type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = ' '
	}
	return len(p), nil
}

func TestFetchBalance_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
			return
		case <-time.After(200 * time.Millisecond):
		}
		io.WriteString(w, `{}`)
	}))
	defer srv.Close()

	withBalanceBase(t, srv, func() {
		withBalanceTimeout(t, 50*time.Millisecond, func() {
			b, err := fetchBalance(context.Background(), testAPIKey)
			require.Nil(t, b)
			require.Equal(t, codeTimeout, errCode(err))
			assertSecretFree(t, err)
		})
	})
}

func TestFetchBalance_NetworkError(t *testing.T) {
	// A closed server: connection refused surfaces as a network error, not a
	// domain code.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close()

	old := balanceAPIBase
	balanceAPIBase = url
	t.Cleanup(func() { balanceAPIBase = old })

	b, err := fetchBalance(context.Background(), testAPIKey)
	require.Nil(t, b)
	require.Equal(t, codeNetwork, errCode(err))
	assertSecretFree(t, err)
}

// errCode extracts the machine-readable code from a fetchBalance error.
func errCode(err error) string {
	if be, ok := err.(*BalanceError); ok {
		return be.Code
	}
	return ""
}

// assertSecretFree asserts an error message never echoes the API key or the
// Authorization header name.
func assertSecretFree(t *testing.T, err error) {
	t.Helper()
	msg := err.Error()
	require.NotContains(t, msg, testAPIKey, "error leaks the API key")
	require.NotContains(t, msg, "Authorization", "error leaks the Authorization header")
	require.NotContains(t, msg, "Bearer", "error leaks the credential scheme")
}
