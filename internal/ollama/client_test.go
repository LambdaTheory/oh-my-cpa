package ollama

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func fixtureServer(t *testing.T, status int, body string) *Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Cookie") == "" {
			t.Errorf("missing cookie header")
		}
		writer.WriteHeader(status)
		_, _ = writer.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return NewClient(5*time.Second, WithSettingsURL(server.URL))
}

func windowByName(t *testing.T, windows []Window, name string) Window {
	t.Helper()
	for _, window := range windows {
		if window.Name == name {
			return window
		}
	}
	t.Fatalf("window %q missing in %+v", name, windows)
	return Window{}
}

func TestFetchNormal(t *testing.T) {
	const page = `<!doctype html><html><body>
	<div class="plan-card">Plan: Pro</div>
	<section class="usage">
		<h3>Rolling hourly usage</h3>
		<div><span>42% used</span></div>
		<p>Resets in 3h</p>
	</section>
	<section class="usage">
		<h3>Weekly usage</h3>
		<div><span>88% used</span></div>
		<p>Resets in 6 days</p>
	</section>
	<div>Credit balance: $12.50</div>
	</body></html>`
	client := fixtureServer(t, http.StatusOK, page)
	snapshot, err := client.Fetch(context.Background(), "acct", "cookie-value")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if !snapshot.IsSuccess() || snapshot.Failure != FailureNone {
		t.Fatalf("not ok: %+v", snapshot)
	}
	if snapshot.Plan == "" {
		t.Errorf("plan not extracted")
	}
	if snapshot.Balance != "$12.50" {
		t.Errorf("balance = %q", snapshot.Balance)
	}
	if len(snapshot.Windows) != 2 {
		t.Fatalf("windows = %+v", snapshot.Windows)
	}
	five := windowByName(t, snapshot.Windows, "5h")
	if five.UsedPercent == nil || *five.UsedPercent != 42 {
		t.Errorf("5h percent = %v", five.UsedPercent)
	}
	if five.ResetAt == nil || !five.ResetDerived {
		t.Errorf("5h reset = %v derived=%v", five.ResetAt, five.ResetDerived)
	}
	seven := windowByName(t, snapshot.Windows, "7d")
	if seven.UsedPercent == nil || *seven.UsedPercent != 88 {
		t.Errorf("7d percent = %v", seven.UsedPercent)
	}
	if seven.ResetAt == nil || seven.ResetAt.Before(snapshot.ObservedAt) {
		t.Errorf("7d reset = %v", seven.ResetAt)
	}
}

func TestFetchPlanOnlyIsParseFailure(t *testing.T) {
	client := fixtureServer(t, http.StatusOK, `<html><body><div>Plan: Pro</div><div>Credit balance: $5</div></body></html>`)
	snapshot, err := client.Fetch(context.Background(), "acct", "x")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if snapshot.IsSuccess() || snapshot.Failure != FailureParse {
		t.Fatalf("plan-only page must be a parse failure: %+v", snapshot)
	}
	if snapshot.Plan != "Pro" {
		t.Errorf("auxiliary plan should still be carried: %q", snapshot.Plan)
	}
}

func TestFetchLoginPage(t *testing.T) {
	client := fixtureServer(t, http.StatusOK, `<html><body><form><input type="password" name="password"><title>Sign in</title></form></body></html>`)
	snapshot, err := client.Fetch(context.Background(), "acct", "expired")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if snapshot.IsSuccess() || snapshot.Err != errSessionInvalid || snapshot.Failure != FailureAuth {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}

func TestFetchUnauthorized(t *testing.T) {
	client := fixtureServer(t, http.StatusUnauthorized, "no")
	snapshot, err := client.Fetch(context.Background(), "acct", "x")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if snapshot.IsSuccess() || snapshot.Err != errSessionInvalid || snapshot.Failure != FailureAuth {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}

func TestFetchForbidden(t *testing.T) {
	client := fixtureServer(t, http.StatusForbidden, "no")
	snapshot, _ := client.Fetch(context.Background(), "acct", "x")
	if snapshot.Failure != FailureAuth {
		t.Fatalf("403 must be an auth failure: %+v", snapshot)
	}
}

// TestFetchRedirectOffDomainSendsNoCookie pins that a redirect is observed as an
// auth failure and that the cookie never reaches the redirect target.
func TestFetchRedirectOffDomainSendsNoCookie(t *testing.T) {
	var externalHits int32
	external := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		atomic.AddInt32(&externalHits, 1)
		writer.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(external.Close)

	redirector := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, external.URL+"/login", http.StatusFound)
	}))
	t.Cleanup(redirector.Close)

	client := NewClient(5*time.Second, WithSettingsURL(redirector.URL))
	snapshot, err := client.Fetch(context.Background(), "acct", "super-secret")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if snapshot.IsSuccess() || snapshot.Failure != FailureAuth || snapshot.Err != errSessionInvalid {
		t.Fatalf("redirect must be an auth failure: %+v", snapshot)
	}
	if hits := atomic.LoadInt32(&externalHits); hits != 0 {
		t.Fatalf("the cookie was sent off-domain: %d request(s)", hits)
	}
}

func TestFetchRateLimitedHonoursRetryAfter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Retry-After", "120")
		writer.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(server.Close)
	client := NewClient(5*time.Second, WithSettingsURL(server.URL))
	snapshot, err := client.Fetch(context.Background(), "acct", "x")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if snapshot.Failure != FailureTransport || !snapshot.HasRetryAfter || snapshot.RetryAfter != 120*time.Second {
		t.Fatalf("429 = %+v, want Retry-After 120s", snapshot)
	}
}

func TestFetchMissingFields(t *testing.T) {
	client := fixtureServer(t, http.StatusOK, `<html><body><div>Nothing here at all</div></body></html>`)
	snapshot, err := client.Fetch(context.Background(), "acct", "x")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if snapshot.IsSuccess() || snapshot.Failure != FailureParse || snapshot.Err == "" {
		t.Fatalf("expected a parse failure snapshot, got %+v", snapshot)
	}
}

func TestCookieNeverLeaks(t *testing.T) {
	const secret = "super-secret-cookie-value"
	client := fixtureServer(t, http.StatusOK, `<html><body><h3>Weekly usage</h3><span>50% used</span></body></html>`)
	snapshot, err := client.Fetch(context.Background(), "acct", secret)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if strings.Contains(snapshot.Err, secret) {
		t.Fatal("cookie leaked into Err")
	}
	if strings.Contains(snapshot.AccountID, secret) {
		t.Fatal("cookie leaked into AccountID")
	}
}

// TestFetchContextCancelledPreservesDeadline drives the timeout without a fixed
// sleep: the handler blocks on its own request context, which the client cancels
// when its deadline fires.
func TestFetchContextCancelledPreservesDeadline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		<-request.Context().Done()
	}))
	t.Cleanup(server.Close)
	client := NewClient(5*time.Second, WithSettingsURL(server.URL))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err := client.Fetch(ctx, "acct", "x")
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want DeadlineExceeded", err)
	}
}

func TestFetchContextCancelled(t *testing.T) {
	client := NewClient(5*time.Second, WithSettingsURL("http://127.0.0.1:1/settings"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := client.Fetch(ctx, "acct", "x")
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want Canceled", err)
	}
}

func TestParseResetRelative(t *testing.T) {
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	at, derived, ok := parseReset("6 days", now)
	if !ok || !derived || !at.Equal(now.Add(6*24*time.Hour)) {
		t.Fatalf("reset = %v derived=%v ok=%v", at, derived, ok)
	}
	if _, _, ok := parseReset("whenever", now); ok {
		t.Fatal("garbage must not parse")
	}
}
