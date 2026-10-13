package api

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

const testReadOnlyKey = "read-only-key-value-16"

func callWithBearer(t *testing.T, fixture providerTestFixture, method, path, payload, key string) (int, []byte) {
	t.Helper()
	request, _ := http.NewRequest(method, fixture.baseURL+"/omc/api/v1"+path, strings.NewReader(payload))
	request.Header.Set("Authorization", "Bearer "+key)
	response, err := (&http.Client{}).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	return response.StatusCode, body
}

func TestReadOnlyBearerReachesReadToolsOnly(t *testing.T) {
	fixture := newProviderTestFixture(t)
	fixture.handler.cfg.MCPReadOnlyKey = testReadOnlyKey

	status, body := callWithBearer(t, fixture, "GET", "/capabilities", "", testReadOnlyKey)
	if status != 200 || !strings.Contains(string(body), "quota_list") {
		t.Fatalf("read-only catalog %d %s", status, body)
	}
	for _, name := range []string{"keys_create", "quota_refresh", "oauth_set_status", "providers_set_status"} {
		if strings.Contains(string(body), name) {
			t.Fatalf("read-only catalog offered %s: %s", name, body)
		}
	}

	// A write tool is refused with the stable code rather than executed.
	status, body = callWithBearer(t, fixture, "POST", "/capabilities/invoke", `{"name":"keys_create","arguments":{}}`, testReadOnlyKey)
	if status != 400 || !strings.Contains(string(body), "capability_forbidden") {
		t.Fatalf("read-only write call %d %s", status, body)
	}

	// The management key still sees the write tools.
	status, body = callWithBearer(t, fixture, "GET", "/capabilities", "", "management-secret-value")
	if status != 200 || !strings.Contains(string(body), "keys_create") {
		t.Fatalf("management catalog %d %s", status, body)
	}

	// A wrong read-only key is refused like any wrong management key.
	status, _ = callWithBearer(t, fixture, "GET", "/capabilities", "", "read-only-key-value-xx")
	if status != 401 {
		t.Fatalf("wrong read-only key accepted: %d", status)
	}
}

func TestReadOnlyMCPCatalogExcludesNonReadTools(t *testing.T) {
	fixture := newProviderTestFixture(t)
	fixture.handler.cfg.MCPReadOnlyKey = testReadOnlyKey
	if err := fixture.handler.ensureAgent(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	readOnlyCatalog, err := executorBackend{handler: fixture.handler, principal: fixture.handler.readOnlyPrincipal()}.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(readOnlyCatalog) == 0 {
		t.Fatal("read-only catalog is empty")
	}
	for _, definition := range readOnlyCatalog {
		if definition.Permission != "read" {
			t.Fatalf("read-only catalog offered %s (%s)", definition.Name, definition.Permission)
		}
	}
	names := map[string]bool{}
	for _, definition := range readOnlyCatalog {
		names[definition.Name] = true
	}
	if names["quota_refresh"] {
		t.Fatal("read-only catalog offered quota_refresh")
	}
	if !names["quota_observations_list"] {
		t.Fatal("read-only catalog is missing quota_observations_list")
	}

	managementCatalog, err := executorBackend{handler: fixture.handler, principal: fixture.handler.mcpPrincipal()}.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	foundWrite := false
	for _, definition := range managementCatalog {
		if definition.Permission == "write" {
			foundWrite = true
		}
	}
	if !foundWrite {
		t.Fatal("management catalog lost its write tools")
	}
}
