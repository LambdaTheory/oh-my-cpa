package ollama

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// maxBodyBytes bounds how much of the settings page is read, so a hostile or
// runaway response cannot exhaust memory.
const maxBodyBytes = 4 << 20

// Client fetches and parses the Ollama settings page. The target URL is a fixed
// constant unless a test overrides it; there is no path by which an operator can
// point it at an arbitrary address.
type Client struct {
	http *http.Client
	url  string
	host string
	// now is injectable so a reset can be anchored without reading the wall clock.
	now func() time.Time
}

// ClientOption adjusts a Client at construction. Only tests use WithSettingsURL.
type ClientOption func(*Client)

// WithSettingsURL points the client at a test fixture server. Production leaves it
// unset so the fixed settings URL is used.
func WithSettingsURL(rawURL string) ClientOption {
	return func(c *Client) {
		trimmed := strings.TrimSpace(rawURL)
		if trimmed == "" {
			return
		}
		c.url = trimmed
		c.host = hostOf(trimmed)
	}
}

// WithHTTPClient injects a transport. The injected client is copied and its
// redirect policy replaced, so even a test client never follows a redirect off the
// settings origin with the cookie attached.
func WithHTTPClient(client *http.Client) ClientOption {
	return func(c *Client) {
		if client == nil {
			return
		}
		replaced := *client
		replaced.CheckRedirect = refuseRedirects
		c.http = &replaced
	}
}

// WithClock injects the clock used to anchor relative resets.
func WithClock(now func() time.Time) ClientOption {
	return func(c *Client) {
		if now != nil {
			c.now = now
		}
	}
}

// NewClient builds a client for the fixed Ollama settings origin.
func NewClient(timeout time.Duration, options ...ClientOption) *Client {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	client := &Client{
		url:  SettingsURL,
		host: hostOf(SettingsURL),
		now:  time.Now,
		http: &http.Client{
			Timeout:       timeout,
			CheckRedirect: refuseRedirects,
		},
	}
	for _, option := range options {
		option(client)
	}
	return client
}

// refuseRedirects stops the transport from following a 3xx. A redirect to the login
// page must be observed as an auth failure, and a redirect off the settings origin
// must never receive the cookie.
func refuseRedirects(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

// Fetch retrieves one account's quota view.
//
// A transport failure is returned as a wrapped Go error (preserving
// context.DeadlineExceeded). Auth, rate-limit and parse failures return a snapshot
// with a structured Failure and a scrubbed Err, never an error. Success requires at
// two validated USD amounts or a legacy numeric window. Plan text alone must
// never make a redesigned page look healthy.
func (c *Client) Fetch(ctx context.Context, accountID, cookie string) (Snapshot, error) {
	snapshot := Snapshot{AccountID: accountID, ObservedAt: c.now()}
	if strings.TrimSpace(cookie) == "" {
		snapshot.Failure = FailureAuth
		snapshot.Err = errSessionInvalid
		return snapshot, nil
	}
	body, status, header, err := c.get(ctx, cookie)
	if err != nil {
		return snapshot, err
	}
	switch {
	case status >= 300 && status < 400:
		snapshot.Failure = FailureAuth
		snapshot.Err = errSessionInvalid
		return snapshot, nil
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		snapshot.Failure = FailureAuth
		snapshot.Err = errSessionInvalid
		return snapshot, nil
	case status == http.StatusTooManyRequests:
		snapshot.Failure = FailureTransport
		snapshot.Err = "Ollama rate limited the settings request"
		snapshot.RetryAfter, snapshot.HasRetryAfter = parseRetryAfter(header)
		return snapshot, nil
	case status != http.StatusOK:
		snapshot.Failure = FailureTransport
		snapshot.Err = fmt.Sprintf("Ollama settings request failed (HTTP %d)", status)
		return snapshot, nil
	}
	if looksLikeLogin(body) {
		snapshot.Failure = FailureAuth
		snapshot.Err = errSessionInvalid
		return snapshot, nil
	}
	result := parse(string(body), snapshot.ObservedAt)
	snapshot.Plan = result.plan
	snapshot.Credits = result.credits
	snapshot.Balance = result.balance
	snapshot.Windows = result.windows
	if !snapshot.IsSuccess() {
		snapshot.Failure = FailureParse
		snapshot.Err = "no trustworthy quota reading could be parsed from the Ollama settings page"
		return snapshot, nil
	}
	snapshot.Windows = result.windows
	return snapshot, nil
}

// get performs the request with the cookie header. The returned error never
// contains the cookie value.
func (c *Client) get(ctx context.Context, cookie string) ([]byte, int, http.Header, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return nil, 0, nil, fmt.Errorf("ollama: build settings request: %w", err)
	}
	if c.host != "" && request.URL.Host != c.host {
		return nil, 0, nil, fmt.Errorf("ollama: refusing request to non-settings host %q", request.URL.Host)
	}
	// A configured value that already contains '=' is a full Cookie header; otherwise
	// it is wrapped as the __Secure-session cookie.
	if strings.Contains(cookie, "=") {
		request.Header.Set("Cookie", cookie)
	} else {
		request.Header.Set("Cookie", "__Secure-session="+cookie)
	}
	request.Header.Set("Accept", "text/html,application/xhtml+xml")
	response, err := c.http.Do(request)
	if err != nil {
		return nil, 0, nil, fmt.Errorf("ollama: GET settings: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBodyBytes))
	if err != nil {
		return nil, response.StatusCode, response.Header, fmt.Errorf("ollama: read settings: %w", err)
	}
	return body, response.StatusCode, response.Header, nil
}

// looksLikeLogin detects the login page without relying on a single marker.
func looksLikeLogin(body []byte) bool {
	text := strings.ToLower(string(body))
	for _, marker := range []string{
		`type="password"`,
		"sign in to ollama",
		"sign in with",
		"<title>sign in",
		"log in to ollama",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

// parseRetryAfter reads the Retry-After header, accepting delay-seconds and an HTTP
// date, bounded to one day so a hostile value cannot park the account indefinitely.
func parseRetryAfter(header http.Header) (time.Duration, bool) {
	for name, values := range header {
		if !strings.EqualFold(name, "Retry-After") {
			continue
		}
		for _, raw := range values {
			value := strings.TrimSpace(raw)
			if value == "" {
				continue
			}
			if seconds, err := strconv.Atoi(value); err == nil {
				if seconds < 0 {
					continue
				}
				return clampBackoff(time.Duration(seconds) * time.Second), true
			}
			if when, err := http.ParseTime(value); err == nil {
				return clampBackoff(time.Until(when)), true
			}
		}
	}
	return 0, false
}

func clampBackoff(wait time.Duration) time.Duration {
	if wait < 0 {
		return 0
	}
	if wait > 24*time.Hour {
		return 24 * time.Hour
	}
	return wait
}

func hostOf(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return ""
	}
	return parsed.Host
}
