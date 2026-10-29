package ollama

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/oh-my-cpa/oh-my-cpa/internal/quota"
)

// BalanceURL is the documented balance endpoint. Like the settings page it is a
// fixed constant, never an operator-supplied address, and the request carries the
// account's API key in a Bearer header.
const BalanceURL = "https://ollama.com/api/balance"

// maxExactCents bounds a reading so remaining + used stays an exact integer in the
// JavaScript wire consumer (Number.MAX_SAFE_INTEGER).
const maxExactCents = 9_007_199_254_740_991

// scrubbed messages for the key path. They name the path a reader has to fix and
// never carry the key or any part of it.
const (
	errBalanceUnauthorized = "Ollama rejected the API key for the balance endpoint (HTTP 401: unauthorized)"
	errBalanceForbidden    = "Ollama refused the API key for the balance endpoint (HTTP 403: forbidden)"
	errBalanceRedirected   = "Ollama redirected the balance request that carried the API key; redirects are not followed"
	errBalanceParse        = "the Ollama balance endpoint returned a response that could not be read"
	errBalanceIncomplete   = "the Ollama balance endpoint returned no included balance for this API key"
)

// usdAmount accepts either a JSON number or a decimal string: the endpoint has been
// observed to vary between the two spellings. A null, an absent field and an empty
// string all stay absent, so a missing figure is never read as zero.
type usdAmount struct {
	value   float64
	present bool
}

func (a *usdAmount) UnmarshalJSON(data []byte) error {
	raw := strings.TrimSpace(string(data))
	if raw == "null" {
		return nil
	}
	if strings.HasPrefix(raw, `"`) {
		var text string
		if err := json.Unmarshal(data, &text); err != nil {
			return err
		}
		raw = strings.TrimSpace(text)
	}
	if raw == "" {
		return nil
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return err
	}
	a.value = value
	a.present = true
	return nil
}

// balanceResponse is the subset of the documented payload this module reads.
//
// purchased.balance_usd is a separate credit balance, not part of the included
// allowance, so it is deliberately not added to remaining, used or total.
type balanceResponse struct {
	Included  *balanceIncluded  `json:"included"`
	Purchased *balancePurchased `json:"purchased"`
}

type balanceIncluded struct {
	BalanceUSD   *usdAmount     `json:"balance_usd"`
	AllowanceUSD *usdAmount     `json:"allowance_usd"`
	Period       *balancePeriod `json:"period"`
}

type balancePurchased struct {
	BalanceUSD *usdAmount `json:"balance_usd"`
}

type balancePeriod struct {
	From  string `json:"from"`
	Until string `json:"until"`
}

// parseBalance maps the balance payload onto the console's shared credits shape.
//
// remaining is included.balance_usd; used is allowance_usd - included.balance_usd,
// clamped at zero. Both must be present for a reading: a payload without an included
// section keeps used nil rather than defaulting it to zero. The total and percentage
// follow the same rule as the settings-page wallet.
func parseBalance(body []byte) (*quota.OllamaCredits, error) {
	var payload balanceResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, errors.New("the balance response body is not valid JSON")
	}
	credits := &quota.OllamaCredits{Source: "balance_api"}
	if payload.Included == nil {
		return credits, nil
	}
	remaining, err := centsFromAmount(payload.Included.BalanceUSD)
	if err != nil {
		return nil, fmt.Errorf("included.balance_usd is unusable: %w", err)
	}
	allowance, err := centsFromAmount(payload.Included.AllowanceUSD)
	if err != nil {
		return nil, fmt.Errorf("included.allowance_usd is unusable: %w", err)
	}
	credits.RemainingCents = remaining
	if remaining == nil || allowance == nil {
		return credits, nil
	}
	used := *allowance - *remaining
	if used < 0 {
		used = 0
	}
	credits.UsedCents = &used
	total := *remaining + used
	credits.TotalCents = &total
	if total > 0 && used > 0 {
		percent := float64(used) / float64(total) * 100
		credits.UsedPercent = &percent
	}
	return credits, nil
}

// centsFromAmount converts one optional dollar amount to integer cents. Absent stays
// absent; a negative, non-finite or oversized figure is refused instead of being
// coerced into a believable number.
func centsFromAmount(amount *usdAmount) (*int64, error) {
	if amount == nil || !amount.present {
		return nil, nil
	}
	if math.IsNaN(amount.value) || math.IsInf(amount.value, 0) {
		return nil, errors.New("the amount is not a finite number")
	}
	if amount.value < 0 {
		return nil, errors.New("the amount is negative")
	}
	cents := math.Round(amount.value * 100)
	if cents > maxExactCents {
		return nil, errors.New("the amount is too large")
	}
	result := int64(cents)
	return &result, nil
}

// FetchBalance reads one account's quota from the documented balance endpoint with
// its API key. A transport failure is a Go error; auth, rate-limit and parse
// failures are scrubbed snapshot values, exactly like the settings-page fetch.
func (c *Client) FetchBalance(ctx context.Context, accountID, apiKey string) (Snapshot, error) {
	snapshot := Snapshot{AccountID: accountID, ObservedAt: c.now()}
	if strings.TrimSpace(apiKey) == "" {
		snapshot.Failure = FailureAuth
		snapshot.Err = "no Ollama API key is stored for the balance endpoint"
		return snapshot, nil
	}
	body, status, header, err := c.getBalance(ctx, apiKey)
	if err != nil {
		return snapshot, err
	}
	switch {
	case status >= 300 && status < 400:
		snapshot.Failure = FailureAuth
		snapshot.Err = errBalanceRedirected
		return snapshot, nil
	case status == http.StatusUnauthorized:
		snapshot.Failure = FailureAuth
		snapshot.Err = errBalanceUnauthorized
		return snapshot, nil
	case status == http.StatusForbidden:
		snapshot.Failure = FailureAuth
		snapshot.Err = errBalanceForbidden
		return snapshot, nil
	case status == http.StatusTooManyRequests:
		snapshot.Failure = FailureTransport
		snapshot.Err = "Ollama rate limited the balance request"
		snapshot.RetryAfter, snapshot.HasRetryAfter = parseRetryAfter(header)
		return snapshot, nil
	case status != http.StatusOK:
		snapshot.Failure = FailureTransport
		snapshot.Err = fmt.Sprintf("Ollama balance request failed (HTTP %d)", status)
		return snapshot, nil
	}
	credits, parseErr := parseBalance(body)
	if parseErr != nil {
		snapshot.Failure = FailureParse
		snapshot.Err = fmt.Sprintf("%s: %v", errBalanceParse, parseErr)
		return snapshot, nil
	}
	snapshot.Credits = credits
	if !snapshot.IsSuccess() {
		snapshot.Failure = FailureParse
		snapshot.Err = errBalanceIncomplete
		return snapshot, nil
	}
	return snapshot, nil
}

// getBalance performs the Bearer request. The key travels only in this header and is
// never included in an error, a log line or a returned value.
func (c *Client) getBalance(ctx context.Context, apiKey string) ([]byte, int, http.Header, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.balanceURL, nil)
	if err != nil {
		return nil, 0, nil, fmt.Errorf("ollama: build balance request: %w", err)
	}
	if c.balanceHost != "" && request.URL.Host != c.balanceHost {
		return nil, 0, nil, fmt.Errorf("ollama: refusing balance request to non-balance host %q", request.URL.Host)
	}
	request.Header.Set("Authorization", "Bearer "+apiKey)
	request.Header.Set("Accept", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		return nil, 0, nil, fmt.Errorf("ollama: GET balance: %w", err)
	}
	defer response.Body.Close()
	body, err := boundedBody(response)
	if err != nil {
		return nil, response.StatusCode, response.Header, fmt.Errorf("ollama: read balance: %w", err)
	}
	return body, response.StatusCode, response.Header, nil
}
