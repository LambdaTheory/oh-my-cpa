package api

import (
	"context"
	"fmt"
	"net/http"
	"testing"
)

func TestManagementLegacyCreateRejectsStoppedRepoKeyAsConflict(t *testing.T) {
	for _, category := range []string{"leaked", "rotate"} {
		t.Run(category, func(t *testing.T) {
			fixture := newProviderTestFixture(t)
			service := fixture.handler.operationsService()
			applied, err := service.ProxyRepoKeyApplication(context.Background(), "org/legacy-http", "synthetic use", repoKeyPublicKey(t), "", "agent", "operator")
			if err != nil {
				t.Fatal(err)
			}
			approved, err := service.ApproveRepoKeyApplication(context.Background(), applied.ApplicationID, "operator", "")
			if err != nil {
				t.Fatal(err)
			}
			key := fixture.state.clientKeys[len(fixture.state.clientKeys)-1]
			if _, err := service.StopRepoKeyBinding(context.Background(), approved.BindingID, "operator", category, "synthetic stop"); err != nil {
				t.Fatal(err)
			}
			response, _ := doJSON(t, fixture.client, http.MethodPost,
				fixture.baseURL+"/omc/api/v1/management/api-keys", fmt.Sprintf(`{"key":%q}`, key))
			if response.StatusCode != http.StatusConflict {
				t.Errorf("stopped repository key creation status=%d, want 409", response.StatusCode)
			}
			for _, active := range fixture.state.clientKeys {
				if active == key {
					t.Fatal("legacy creation made a stopped repository key usable")
				}
			}
			response, _ = doJSON(t, fixture.client, http.MethodPost,
				fixture.baseURL+"/omc/api/v1/management/api-keys", `{"key":"sk-synthetic-unbound-http-regression"}`)
			if response.StatusCode != http.StatusOK {
				t.Fatalf("unbound legacy creation status=%d, want 200", response.StatusCode)
			}
		})
	}
}
