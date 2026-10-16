package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/oh-my-cpa/oh-my-cpa/internal/operations"
	"github.com/oh-my-cpa/oh-my-cpa/internal/quota"
	"github.com/oh-my-cpa/oh-my-cpa/internal/repository"
)

func TestModelAdviceReadOnlyMCPUsesStoredQuotaWithoutProbes(t *testing.T) {
	fixture := newProviderTestFixture(t)
	fixture.handler.cfg.MCPReadOnlyKey = testReadOnlyKey
	upstream := newFakeCPA(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			t.Errorf("advice attempted mutation: %s", request.Method)
			writer.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		switch request.URL.Path {
		case "/v8/management/config":
			io.WriteString(writer, `{"access":{"api-keys":["synthetic-directory-key"]}}`)
		case "/v1/models":
			io.WriteString(writer, `{"data":[{"id":"claude-sonnet-4"},{"id":"configured-only"}]}`)
		case "/v0/management/codex-api-key":
			io.WriteString(writer, `{"codex-api-key":[{"auth-index":"synthetic-config-index","api-key":"synthetic-private-config-key","models":[{"name":"gpt-5","alias":"configured-only"}]}]}`)
		case "/v8/management/config/oauth/model-alias":
			io.WriteString(writer, `{}`)
		case "/v8/management/credentials":
			io.WriteString(writer, `{"files":[{"auth_index":"synthetic-advice-index","provider":"claude","name":"private-account@example.test.json","models":[{"id":"claude-sonnet-4"}]}]}`)
		case "/v0/management/openai-compatibility":
			io.WriteString(writer, `{"openai-compatibility":[]}`)
		default:
			if strings.HasPrefix(request.URL.Path, "/v0/management/") && strings.HasSuffix(request.URL.Path, "-api-key") {
				io.WriteString(writer, `{}`)
			} else {
				t.Errorf("advice reached an undeclared endpoint or quota probe")
				writer.WriteHeader(http.StatusNotFound)
			}
		}
	}))
	defer upstream.Close()
	pointPlaygroundAt(t, fixture.handler, upstream.URL)
	if err := fixture.handler.repo.SaveQuotaSnapshot(context.Background(), repository.QuotaSnapshotRecord{
		AuthIndex: "synthetic-advice-index", Provider: "claude", Status: "healthy",
		ObservedAtMS: fixture.handler.now().UnixMilli(),
		WindowsJSON:  `[{"id":"five_hour","label":"private-account@example.test.json","kind":"five_hour","scope":"standard","used_percent":10,"remaining_percent":90}]`,
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "advice-test", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint: fixture.baseURL + "/omc/api/mcp", DisableStandaloneSSE: true,
		HTTPClient: &http.Client{Transport: bearerTransport{key: testReadOnlyKey}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "model_advice", Arguments: map[string]any{"models": []string{"claude-sonnet-4"}}})
	if err != nil || result.IsError {
		t.Fatalf("read-only advice: %+v, %v", result, err)
	}
	text := result.Content[0].(*mcp.TextContent).Text
	var envelope struct {
		Status string                       `json:"status"`
		Data   operations.ModelAdviceResult `json:"data"`
	}
	if err := json.Unmarshal([]byte(text), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Status != "success" || len(envelope.Data.Models) != 1 || envelope.Data.Models[0].Verdict != "ok" || envelope.Data.Models[0].CredentialsUsable != 1 {
		t.Fatalf("stored advice = %+v", envelope)
	}
	for _, forbidden := range []string{"private-account", "@example.test", "synthetic-advice-index", "synthetic-directory-key", "auth_index", "file_path"} {
		if strings.Contains(text, forbidden) {
			t.Errorf("read-only advice leaked %q", forbidden)
		}
	}
	result, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "model_advice", Arguments: map[string]any{"models": []string{"configured-only"}}})
	if err != nil || result.IsError {
		t.Fatalf("configured-only advice: %+v, %v", result, err)
	}
	if err := json.Unmarshal([]byte(result.Content[0].(*mcp.TextContent).Text), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.Models[0].Verdict != "unknown" || envelope.Data.Models[0].CredentialsTotal != 1 {
		t.Fatalf("configured key without snapshot = %+v", envelope.Data.Models[0])
	}
	fixture.handler.quotaBackoff = quota.NewBackoff(time.Second, time.Minute)
	fixture.handler.quotaBackoff.Record("claude", "another-synthetic-index", time.Minute, true, fixture.handler.now())
	result, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "model_advice", Arguments: map[string]any{"models": []string{"claude-sonnet-4"}}})
	if err != nil || result.IsError {
		t.Fatalf("provider-backoff advice: %+v, %v", result, err)
	}
	if err := json.Unmarshal([]byte(result.Content[0].(*mcp.TextContent).Text), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.Models[0].Verdict != "avoid" || envelope.Data.Models[0].CredentialsUsable != 0 {
		t.Fatalf("provider backoff did not block advice: %+v", envelope.Data.Models[0])
	}
	fixture.handler.quotaBackoff.Clear("claude", "another-synthetic-index")
	if err := fixture.handler.repo.SaveFeishuAccountSignal(ctx, repository.FeishuAccountSignal{
		AuthIndex: "synthetic-advice-index", Provider: "claude", State: repository.FeishuSignalAuthInvalid,
		ObservedAt: fixture.handler.now().Add(time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	result, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "model_advice", Arguments: map[string]any{"models": []string{"claude-sonnet-4"}}})
	if err != nil || result.IsError {
		t.Fatalf("failed-refresh advice: %+v, %v", result, err)
	}
	if err := json.Unmarshal([]byte(result.Content[0].(*mcp.TextContent).Text), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.Models[0].Verdict != "unknown" || envelope.Data.Models[0].CredentialsUsable != 0 {
		t.Fatalf("old successful snapshot survived a newer authentication failure: %+v", envelope.Data.Models[0])
	}
}
