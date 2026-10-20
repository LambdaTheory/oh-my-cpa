package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestResetAnnouncementCreateListAndExpiry(t *testing.T) {
	var clock atomic.Int64
	clock.Store(time.Date(2026, 10, 20, 8, 0, 0, 0, time.UTC).UnixMilli())
	client, base, _ := startDashboardTestServer(t, func(_ http.ResponseWriter, request *http.Request) {
		t.Errorf("unexpected CPA request: %s", request.URL.Path)
	}, func(h *Handler) { h.nowFn = func() time.Time { return time.UnixMilli(clock.Load()) } })
	endpoint := base + "/omc/api/v1/management/quota/reset-announcements"
	create := func(body string) resetAnnouncementDTO {
		t.Helper()
		response, err := client.Post(endpoint, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var item resetAnnouncementDTO
		if err = json.NewDecoder(response.Body).Decode(&item); err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != 201 || item.ID == "" || item.Status != "active" || item.RegisteredAtMS != clock.Load() || item.CreatedAtMS != clock.Load() {
			t.Fatalf("create status=%d item=%+v", response.StatusCode, item)
		}
		return item
	}
	timed := create(fmt.Sprintf(`{"provider":"codex","source":"manual","scope":"all","confidence":"confirmed","expected_reset_at_ms":%d,"original_text":"Public reset notice"}`, clock.Load()+1000))
	clock.Add(1)
	untimed := create(`{"provider":"claude","source":"manual","scope":"model","model":"sonnet","confidence":"unverified","expected_reset_at_ms":null,"original_text":"Unverified notice"}`)
	list := func(query string) resetAnnouncementListDTO {
		t.Helper()
		response, body := getJSON(t, client, endpoint+query)
		if response.StatusCode != 200 || response.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("list status=%d body=%s", response.StatusCode, body)
		}
		var result resetAnnouncementListDTO
		if err := json.Unmarshal(body, &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	page := list("?limit=1")
	if page.Total != 2 || len(page.Items) != 1 || page.Items[0].ID != untimed.ID || page.Items[0].Model != "sonnet" {
		t.Fatalf("first page: %+v", page)
	}
	page = list("?limit=1&offset=1")
	if page.Total != 2 || len(page.Items) != 1 || page.Items[0].ID != timed.ID {
		t.Fatalf("second page: %+v", page)
	}
	clock.Add(999)
	page = list("?status=active")
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID != untimed.ID || page.Items[0].Confidence != "unverified" {
		t.Fatalf("active page: %+v", page)
	}
	page = list("?status=expired")
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID != timed.ID || page.Items[0].Status != "expired" {
		t.Fatalf("expired page: %+v", page)
	}
	if page = list(""); page.Total != 2 {
		t.Fatalf("history lost: %+v", page)
	}
	for _, query := range []string{"?status=rejected", "?limit=0", "?limit=101", "?offset=-1", "?offset=no", "?limit="} {
		response, body := getJSON(t, client, endpoint+query)
		if response.StatusCode != 400 {
			t.Fatalf("query=%s status=%d body=%s", query, response.StatusCode, body)
		}
	}
}

func TestResetAnnouncementRejectsInvalidWritesAndNonSessionCallers(t *testing.T) {
	now := time.Date(2026, 10, 20, 8, 0, 0, 0, time.UTC)
	client, base, _ := startDashboardTestServer(t, nil, func(h *Handler) { h.nowFn = func() time.Time { return now } })
	endpoint := base + "/omc/api/v1/management/quota/reset-announcements"
	valid := map[string]any{"provider": "codex", "source": "manual", "scope": "all", "confidence": "probable", "original_text": "Public announcement"}
	invalid := []struct {
		name   string
		change func(map[string]any)
	}{}
	for _, field := range []string{"provider", "source", "scope", "confidence", "original_text"} {
		invalid = append(invalid, struct {
			name   string
			change func(map[string]any)
		}{"missing " + field, func(input map[string]any) { delete(input, field) }})
	}
	for field, values := range map[string][]any{
		"provider": {"Codex", " codex", "a/b", "1codex", strings.Repeat("a", 65)},
		"source":   {"status_rss", "x_api", "unknown"}, "scope": {"model", "credential", "unknown"},
		"confidence": {"trusted"}, "original_text": {"  ", strings.Repeat("a", 4001), "null\x00character"},
		"expected_reset_at_ms": {now.UnixMilli(), now.UnixMilli() - 1, "tomorrow", 253402300800000},
		"status":               {"rejected"}, "model": {"unexpected"},
	} {
		for _, value := range values {
			invalid = append(invalid, struct {
				name   string
				change func(map[string]any)
			}{fmt.Sprintf("%s=%v", field, value), func(input map[string]any) { input[field] = value }})
		}
	}
	for _, scenario := range invalid {
		t.Run(scenario.name, func(t *testing.T) {
			input := map[string]any{}
			for key, value := range valid {
				input[key] = value
			}
			scenario.change(input)
			body, _ := json.Marshal(input)
			response, err := client.Post(endpoint, "application/json", bytes.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != 400 {
				t.Fatalf("status=%d", response.StatusCode)
			}
		})
	}
	for _, body := range []string{"null", "{} {}", strings.Repeat(" ", 33<<10)} {
		response, err := client.Post(endpoint, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 400 {
			t.Fatalf("malformed status=%d", response.StatusCode)
		}
	}
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		request, _ := http.NewRequest(method, endpoint, strings.NewReader("{}"))
		request.Header.Set("Authorization", "Bearer synthetic-agent-token")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 401 {
			t.Fatalf("bearer %s status=%d", method, response.StatusCode)
		}
	}
	request, _ := http.NewRequest(http.MethodPost, endpoint, strings.NewReader("{}"))
	request.Header.Set("Origin", "https://other.invalid")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 403 {
		t.Fatalf("cross-origin status=%d", response.StatusCode)
	}
	response, body := getJSON(t, client, endpoint)
	if response.StatusCode != 200 || !strings.Contains(string(body), `"total":0`) {
		t.Fatalf("invalid writes persisted: %s", body)
	}
}
