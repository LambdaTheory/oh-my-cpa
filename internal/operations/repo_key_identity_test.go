package operations

import (
	"context"
	"testing"
)

func TestRepoKeyGitHubCaseVariantsReuseOneKeyAndPreserveDisplay(t *testing.T) {
	service, gateway := newRepoKeyService(t)
	first := approveRegressionRepoKey(t, service, "https://github.com/T14-Fixture/Case-Repo.git", "first-agent", "device")
	if first.RepoID != "t14-fixture/case-repo" || first.RepoDisplay != "T14-Fixture/Case-Repo" {
		t.Fatalf("identity/display were mixed: repo=%q display=%q", first.RepoID, first.RepoDisplay)
	}
	originalKeys := gateway.clientKeys()
	_, publicKey := newCallerKeyPair(t)
	ctx := callerContext("second-agent", "device")
	second, err := service.ApplyRepoKey(ctx, RepoKeyApplyInput{Repo: "git@github.com:t14-fixture/case-repo.git", Purpose: "same repository", PublicKey: publicKey})
	if err != nil || second.Repo != first.RepoID || second.Kind != "reuse" || second.Status != "pending" {
		t.Fatalf("case variant did not reuse the existing identity: repo=%q kind=%s status=%s err=%v", second.Repo, second.Kind, second.Status, err)
	}
	approved, err := service.ApproveRepoKeyApplication(context.Background(), second.ApplicationID, "operator", "")
	if err != nil || approved.BindingID != first.BindingID || len(gateway.clientKeys()) != len(originalKeys) {
		t.Fatal("a GitHub case variant issued a second key")
	}
	status, err := service.RepoKeyStatus(ctx, second.ApplicationID)
	if err != nil || status.Repo != first.RepoID || status.RepoDisplay != "t14-fixture/case-repo" {
		t.Fatal("application status lost its identity or submitted spelling")
	}
}
