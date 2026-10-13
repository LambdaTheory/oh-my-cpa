package operations

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oh-my-cpa/oh-my-cpa/internal/capability"
	"github.com/oh-my-cpa/oh-my-cpa/internal/repository"
)

func TestMaskAccountLabelShapes(t *testing.T) {
	cases := map[string]string{
		"vinsprite78@gmail.com":              "vin***@gmail.com",
		"user@example.com":                   "use***@example.com",
		"ab@example.com":                     "a***@example.com",
		"a@example.com":                      "a***@example.com",
		"codex-user@gmail.com-plus (1).json": "cod***@gmail.com-plus (1).json",
		"no-email-here":                      "no-email-here",
		"two@a.com and three@b.org":          "t***@a.com and thr***@b.org",
	}
	for input, want := range cases {
		if got := maskAccountLabel(input); got != want {
			t.Fatalf("maskAccountLabel(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestAccountLabelMaskedOnlyForReadOnlyCallers(t *testing.T) {
	value := "vinsprite78@gmail.com"
	if got := accountLabelForCaller(context.Background(), value); got != value {
		t.Fatalf("full-authority label = %q, want unchanged", got)
	}
	readOnly := capability.WithReadOnly(context.Background(), true)
	if got := accountLabelForCaller(readOnly, value); got != "vin***@gmail.com" {
		t.Fatalf("read-only label = %q", got)
	}
}

func TestQuotaListMasksAccountLabelsForReadOnlyCallers(t *testing.T) {
	ctx := context.Background()
	database, err := repository.Open(ctx, filepath.Join(t.TempDir(), "masking.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	repo := repository.New(database)
	service := &Service{Repo: repo, Quotas: func(context.Context) ([]Quota, error) {
		return []Quota{{AuthIndex: "c-1", Name: "vinsprite78@gmail.com", Provider: "codex"}}, nil
	}}
	registry := capability.NewRegistry()
	if err := service.registerQuota(registry); err != nil {
		t.Fatal(err)
	}
	executor := &capability.Executor{Registry: registry, Store: repository.AgentStore{Repo: repo}, Authorize: func(_ context.Context, id, _ string) bool {
		return id == "administrator" || id == "mcp-readonly"
	}}
	readOnly := capability.Principal{ID: "mcp-readonly", Adapter: "mcp", IsReadOnly: true, Allowed: map[string]bool{"quota_list": true}}
	result, err := executor.Invoke(ctx, readOnly, "quota_list", json.RawMessage(`{}`), "")
	if err != nil || result.Status != "success" {
		t.Fatalf("read-only quota_list: %+v %v", result, err)
	}
	if strings.Contains(string(result.Data), "vinsprite78@gmail.com") {
		t.Fatalf("read-only result leaked the address: %s", result.Data)
	}
	if !strings.Contains(string(result.Data), "vin***@gmail.com") {
		t.Fatalf("read-only result was not masked: %s", result.Data)
	}

	admin := capability.Principal{ID: "administrator", Adapter: "agent", IsAdmin: true}
	result, err = executor.Invoke(ctx, admin, "quota_list", json.RawMessage(`{}`), "")
	if err != nil || result.Status != "success" || !strings.Contains(string(result.Data), "vinsprite78@gmail.com") {
		t.Fatalf("full-authority quota_list: %+v %v", result, err)
	}
}
