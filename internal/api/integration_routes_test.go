package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/oh-my-cpa/oh-my-cpa/internal/feishu"
	"github.com/oh-my-cpa/oh-my-cpa/internal/repository"
)

// TestOptionalModuleRoutesRequireTheConsoleSession proves the lane B/C routes are
// console-only: an unauthenticated caller, and a caller presenting a bearer token,
// are both refused before any handler runs.
func TestOptionalModuleRoutesRequireTheConsoleSession(t *testing.T) {
	client, base, _ := startDashboardTestServer(t, func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(request.URL.Path, "/v8/management/credentials") {
			_, _ = writer.Write([]byte(`{"files":[]}`))
			return
		}
		_, _ = writer.Write([]byte(`{}`))
	}, func(handler *Handler) {
		api, err := handler.BuildFeishuAPI(context.Background(), handler.BuildFeishuReportSource(), feishu.Overlay{})
		if err != nil {
			t.Fatalf("build feishu api: %v", err)
		}
		handler.SetFeishuAPI(api)
	})

	anonymous := &http.Client{}
	for _, path := range []string{
		"/omc/api/v1/ollama/accounts",
		"/omc/api/v1/ollama/quota",
		"/omc/api/v1/feishu/settings",
		"/omc/api/v1/feishu/status",
	} {
		response, err := anonymous.Get(base + path)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("anonymous GET %s = %d, want 401", path, response.StatusCode)
		}
	}

	// A bearer token is not a console session for these routes: the read-only MCP key
	// reaches read capabilities, not the management surface.
	request, err := http.NewRequest(http.MethodGet, base+"/omc/api/v1/ollama/accounts", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer read-only-key-that-is-long-enough")
	bearerResponse, err := anonymous.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	bearerResponse.Body.Close()
	if bearerResponse.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bearer GET = %d, want 401", bearerResponse.StatusCode)
	}

	// The authenticated console reaches them.
	settingsResponse, payload := getJSON(t, client, base+"/omc/api/v1/feishu/settings")
	if settingsResponse.StatusCode != http.StatusOK {
		t.Fatalf("settings status = %d, body = %s", settingsResponse.StatusCode, payload)
	}
	if strings.Contains(string(payload), "app_secret") && strings.Contains(string(payload), "\"app_secret\":\"") {
		t.Fatalf("settings response must not carry the secret: %s", payload)
	}
}

// TestOptionalModuleWritesAreSameOriginGuarded proves the management writes keep the
// console's same-origin contract.
func TestOptionalModuleWritesAreSameOriginGuarded(t *testing.T) {
	client, base, _ := startDashboardTestServer(t, func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(request.URL.Path, "/v8/management/credentials") {
			_, _ = writer.Write([]byte(`{"files":[]}`))
			return
		}
		_, _ = writer.Write([]byte(`{}`))
	}, func(handler *Handler) {
		api, err := handler.BuildFeishuAPI(context.Background(), handler.BuildFeishuReportSource(), feishu.Overlay{})
		if err != nil {
			t.Fatalf("build feishu api: %v", err)
		}
		handler.SetFeishuAPI(api)
	})

	body, _ := json.Marshal(map[string]any{"enabled": true})
	writes := []struct {
		method string
		path   string
	}{
		{http.MethodPut, "/omc/api/v1/feishu/settings"},
		{http.MethodPost, "/omc/api/v1/ollama/accounts"},
	}
	for _, write := range writes {
		request, err := http.NewRequest(write.method, base+write.path, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Origin", "http://attacker.test")
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusForbidden {
			t.Fatalf("cross-origin %s %s = %d, want 403", write.method, write.path, response.StatusCode)
		}
	}
}

// TestFeishuTestSendIsRefusedInDemo proves the one lane-C route that would reach
// Feishu is refused by the demo boundary rather than merely hidden.
func TestFeishuTestSendIsRefusedInDemo(t *testing.T) {
	rule, match := demoVerdictFor(http.MethodPost, "/api/v1/feishu/test")
	if match != demoMatchVerdict {
		t.Fatal("the Feishu test route must be classified")
	}
	if rule.verdict != demoRefuse {
		t.Fatalf("Feishu test send must be refused in a demo: %+v", rule)
	}
}

// TestFeishuIncompleteConfigIsInertNotFatal proves a module that is switched on but
// not fully configured never fails the build or startup: it is loaded, reported
// degraded and stays inert.
func TestFeishuIncompleteConfigIsInertNotFatal(t *testing.T) {
	var handler *Handler
	_, _, repo := startDashboardTestServer(t, func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(request.URL.Path, "/v8/management/credentials") {
			_, _ = writer.Write([]byte(`{"files":[]}`))
			return
		}
		_, _ = writer.Write([]byte(`{}`))
	}, func(h *Handler) { handler = h })

	if err := repo.SaveFeishuSettings(context.Background(), repository.FeishuSettings{
		Enabled: true, AppID: "cli_demo", ChatID: "", SummaryTimesJSON: `["10:00"]`,
	}); err != nil {
		t.Fatal(err)
	}
	api, err := handler.BuildFeishuAPI(context.Background(), handler.BuildFeishuReportSource(), feishu.Overlay{})
	if err != nil {
		t.Fatalf("an incomplete configuration must not fail the build: %v", err)
	}
	if api == nil || api.Service() == nil {
		t.Fatal("the module must stay loaded even when degraded")
	}
	status := api.Service().StatusSnapshot(context.Background())
	if status.Enabled || !status.Degraded || status.Ready {
		t.Fatalf("incomplete configuration must be degraded and inert: %+v", status)
	}
}

// TestOptionalModuleResponsesNeverCarrySecrets proves the lane B/C DTO allowlist: a
// stored Feishu app secret and a stored Ollama cookie never appear in any response,
// including the read-only capability summaries' sources.
func TestOptionalModuleResponsesNeverCarrySecrets(t *testing.T) {
	const feishuSecret = "feishu-app-secret-value"
	const ollamaCookie = "ollama-session-cookie-value"

	client, base, _ := startDashboardTestServer(t, func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(request.URL.Path, "/v8/management/credentials") {
			_, _ = writer.Write([]byte(`{"files":[]}`))
			return
		}
		_, _ = writer.Write([]byte(`{}`))
	}, func(handler *Handler) {
		api, err := handler.BuildFeishuAPI(context.Background(), handler.BuildFeishuReportSource(), feishu.Overlay{})
		if err != nil {
			t.Fatalf("build feishu api: %v", err)
		}
		handler.SetFeishuAPI(api)
	})

	// Store a Feishu secret and an Ollama cookie, then read every response that could
	// carry one.
	feishuBody, _ := json.Marshal(map[string]any{
		"enabled": true, "app_id": "cli_demo", "app_secret": feishuSecret, "chat_id": "oc_demo",
	})
	putRequest, err := http.NewRequest(http.MethodPut, base+"/omc/api/v1/feishu/settings", bytes.NewReader(feishuBody))
	if err != nil {
		t.Fatal(err)
	}
	putRequest.Header.Set("Content-Type", "application/json")
	putResponse, err := client.Do(putRequest)
	if err != nil {
		t.Fatal(err)
	}
	putResponse.Body.Close()
	if putResponse.StatusCode != http.StatusOK {
		t.Fatalf("feishu settings write = %d", putResponse.StatusCode)
	}

	ollamaBody, _ := json.Marshal(map[string]any{"label": "work", "cookie": ollamaCookie})
	ollamaRequest, err := http.NewRequest(http.MethodPost, base+"/omc/api/v1/ollama/accounts", bytes.NewReader(ollamaBody))
	if err != nil {
		t.Fatal(err)
	}
	ollamaRequest.Header.Set("Content-Type", "application/json")
	ollamaResponse, err := client.Do(ollamaRequest)
	if err != nil {
		t.Fatal(err)
	}
	ollamaResponse.Body.Close()
	if ollamaResponse.StatusCode != http.StatusCreated {
		t.Fatalf("ollama account write = %d", ollamaResponse.StatusCode)
	}

	for _, path := range []string{
		"/omc/api/v1/feishu/settings",
		"/omc/api/v1/feishu/status",
		"/omc/api/v1/ollama/accounts",
		"/omc/api/v1/ollama/quota",
	} {
		response, payload := getJSON(t, client, base+path)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("GET %s = %d, body = %s", path, response.StatusCode, payload)
		}
		if strings.Contains(string(payload), feishuSecret) {
			t.Fatalf("%s exposed the Feishu app secret: %s", path, payload)
		}
		if strings.Contains(string(payload), ollamaCookie) {
			t.Fatalf("%s exposed the Ollama cookie: %s", path, payload)
		}
	}
}
