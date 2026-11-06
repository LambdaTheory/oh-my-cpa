package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

func TestManagementRepoKeyIdentityPreservesReportedDisplay(t *testing.T) {
	fixture := newProviderTestFixture(t)
	response, payload := doJSON(t, fixture.client, http.MethodPost,
		fixture.baseURL+"/omc/api/v1/management/repo-keys/applications",
		fmt.Sprintf(`{"repo":"https://github.com/T14-Fixture/Case-Repo.git","purpose":"synthetic identity review","agent":"case-agent","public_key":%q}`, repoKeyPublicKey(t)))
	if response.StatusCode != http.StatusOK {
		t.Fatalf("apply status=%d", response.StatusCode)
	}
	var applied struct {
		ID      string `json:"application_id"`
		Repo    string `json:"repo"`
		Display string `json:"repo_display"`
	}
	if err := json.Unmarshal(payload, &applied); err != nil || applied.Repo != "t14-fixture/case-repo" || applied.Display != "T14-Fixture/Case-Repo" {
		t.Fatal("application response mixed canonical identity and reported display")
	}
	response, payload = getJSON(t, fixture.client, fixture.baseURL+"/omc/api/v1/management/repo-keys/applications/"+applied.ID+"/review")
	var review RepoKeyReviewDTO
	if response.StatusCode != http.StatusOK || json.Unmarshal(payload, &review) != nil || review.Application.Repo != applied.Repo || review.Application.RepoDisplay != applied.Display {
		t.Fatal("approval review lost the reported repository spelling")
	}
}
