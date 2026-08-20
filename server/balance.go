package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// DeepSeek balance endpoint contract (GET {apiBase}/user/balance, Bearer auth):
//
//	{
//	  "is_available": true,
//	  "balance_infos": [
//	    {"currency": "CNY", "total_balance": "110.00", "granted_balance": "10.00", "topped_up_balance": "100.00"}
//	  ]
//	}
//
// `currency` is CNY or USD; the three balance fields are strings. Failures are
// classified into machine-readable codes the plugin surfaces verbatim. No
// error string ever contains the API key or the Authorization header.

const (
	// defaultAPIBase is the production DeepSeek API origin.
	defaultAPIBase = "https://api.deepseek.com"

	// maxResponseBytes caps the accepted response body (1 MiB). Anything
	// larger is bad_response, never parsed.
	maxResponseBytes = 1 << 20

	// DeepSeek user/balance resource path.
	balancePath = "/user/balance"
)

// Machine-readable error codes, matching the spec's action contract.
const (
	codeInvalidKey          = "invalid_key"
	codeInsufficientBalance = "insufficient_balance"
	codeRateLimited         = "rate_limited"
	codeTimeout             = "timeout"
	codeNetwork             = "network"
	codeHTTP                = "http"
	codeBadResponse         = "bad_response"
)

// BalanceInfo is one currency entry of the DeepSeek balance payload. The three
// balance fields are documented as strings; the client validates they parse as
// finite decimals before accepting the snapshot (a value like "abc" would
// render as NaN and silently disable the amber threshold).
type BalanceInfo struct {
	Currency        string `json:"currency"`
	TotalBalance    string `json:"total_balance"`
	GrantedBalance  string `json:"granted_balance"`
	ToppedUpBalance string `json:"topped_up_balance"`
}

// Balance is a validated DeepSeek balance snapshot. The slice preserves the
// order DeepSeek returned (the spec's primary currency is the first entry).
type Balance struct {
	IsAvailable  bool
	BalanceInfos []BalanceInfo
}

// BalanceError is a classified fetch failure carrying the machine-readable
// code the action response surfaces. Code is never empty.
type BalanceError struct {
	Code    string
	Message string
}

func (e *BalanceError) Error() string { return e.Message }

// balanceWire is the wire shape with presence-tracking pointers, so a missing
// `is_available` or a missing `balance_infos` key is detectable (both violate
// the documented shape and are bad_response).
type balanceWire struct {
	IsAvailable  *bool         `json:"is_available"`
	BalanceInfos []BalanceInfo `json:"balance_infos"`
}

// balanceAPIBase and balanceTimeout are package-level seams so tests can point
// the client at an httptest server and shrink the deadline; production values
// are the defaults above. balanceTimeout bounds a single fetch: the plugin's
// action route enforces a 15 s host deadline, so one fetch stays well under it.
var (
	balanceAPIBase = defaultAPIBase
	balanceTimeout = 10 * time.Second
)

// fetchBalance calls GET {base}/user/balance with Authorization: Bearer
// <apiKey>, a bounded deadline, and a 1 MiB response cap, and returns a
// validated snapshot. The base URL is injectable via balanceAPIBase for tests.
func fetchBalance(ctx context.Context, apiKey string) (*Balance, error) {
	reqCtx, cancel := context.WithTimeout(ctx, balanceTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, balanceAPIBase+balancePath, nil)
	if err != nil {
		return nil, &BalanceError{Code: codeNetwork, Message: "building DeepSeek balance request: " + err.Error()}
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if reqCtx.Err() != nil {
			return nil, &BalanceError{Code: codeTimeout, Message: "DeepSeek balance request timed out after 10s"}
		}
		return nil, &BalanceError{Code: codeNetwork, Message: "DeepSeek balance request failed: " + err.Error()}
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		// continue to parse below
	case http.StatusUnauthorized:
		return nil, &BalanceError{Code: codeInvalidKey, Message: "DeepSeek rejected the API key (401)"}
	case http.StatusPaymentRequired:
		return nil, &BalanceError{Code: codeInsufficientBalance, Message: "DeepSeek reports insufficient balance (402)"}
	case http.StatusTooManyRequests:
		return nil, &BalanceError{Code: codeRateLimited, Message: "DeepSeek rate limited the request (429)"}
	default:
		return nil, &BalanceError{Code: codeHTTP, Message: fmt.Sprintf("DeepSeek balance endpoint returned HTTP %d", resp.StatusCode)}
	}

	// 1 MiB response-size cap: read at most cap+1 bytes so an oversized body is
	// detected, not silently truncated.
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, &BalanceError{Code: codeNetwork, Message: "reading DeepSeek balance response: " + err.Error()}
	}
	if len(body) > maxResponseBytes {
		return nil, &BalanceError{Code: codeBadResponse, Message: "DeepSeek balance response exceeds the 1 MiB cap"}
	}

	var wire balanceWire
	if err := json.Unmarshal(body, &wire); err != nil {
		return nil, &BalanceError{Code: codeBadResponse, Message: "DeepSeek balance response is not valid JSON"}
	}
	if wire.IsAvailable == nil {
		return nil, &BalanceError{Code: codeBadResponse, Message: "DeepSeek balance response is missing is_available"}
	}
	if wire.BalanceInfos == nil {
		return nil, &BalanceError{Code: codeBadResponse, Message: "DeepSeek balance response is missing balance_infos"}
	}
	for _, info := range wire.BalanceInfos {
		if info.Currency != "CNY" && info.Currency != "USD" {
			return nil, &BalanceError{Code: codeBadResponse, Message: fmt.Sprintf("DeepSeek balance response has unsupported currency %q", info.Currency)}
		}
		if !finiteDecimal(info.TotalBalance) || !finiteDecimal(info.GrantedBalance) || !finiteDecimal(info.ToppedUpBalance) {
			return nil, &BalanceError{Code: codeBadResponse, Message: "DeepSeek balance response has a non-numeric balance field"}
		}
	}

	return &Balance{IsAvailable: *wire.IsAvailable, BalanceInfos: wire.BalanceInfos}, nil
}

// finiteDecimal reports whether s parses as a finite decimal number. A value
// like "abc" (or "NaN"/"Inf") would otherwise render as NaN and silently
// disable the amber threshold, so it is rejected here.
func finiteDecimal(s string) bool {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return false
	}
	return !math.IsNaN(f) && !math.IsInf(f, 0)
}

// errBalanceCode extracts the BalanceError code from err, or "" if err is not
// a classified balance failure.
func errBalanceCode(err error) string {
	var be *BalanceError
	if errors.As(err, &be) {
		return be.Code
	}
	return ""
}
