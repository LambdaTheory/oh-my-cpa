package quota

import (
	"context"
	"errors"
	"testing"

	"github.com/oh-my-cpa/oh-my-cpa/internal/cpa/management"
)

type rosterQuotaClient struct {
	mockCPAClient
	compatibility func(context.Context) (management.OpenAICompatibilityResponse, error)
	configView    func(context.Context) (map[string]any, management.ResponseMeta, error)
}

func (client *rosterQuotaClient) OpenAICompatibility(ctx context.Context) (management.OpenAICompatibilityResponse, error) {
	if client.compatibility == nil {
		return management.OpenAICompatibilityResponse{}, errors.New("openai-compatibility unavailable")
	}
	return client.compatibility(ctx)
}

func (client *rosterQuotaClient) ConfigView(ctx context.Context) (map[string]any, management.ResponseMeta, error) {
	if client.configView == nil {
		return map[string]any{}, management.ResponseMeta{}, nil
	}
	return client.configView(ctx)
}

func rosterCompatibilityClient(entries ...management.OpenAICompatibility) *rosterQuotaClient {
	return &rosterQuotaClient{compatibility: func(context.Context) (management.OpenAICompatibilityResponse, error) {
		return management.OpenAICompatibilityResponse{Entries: entries}, nil
	}}
}

func TestOpenCodeAuthFilesBuildsOneTargetPerIndexedKey(t *testing.T) {
	client := rosterCompatibilityClient(
		management.OpenAICompatibility{Name: "Command Code", BaseURL: "https://api.commandcode.ai/provider/v1"},
		management.OpenAICompatibility{
			Name: " OpenCode ", BaseURL: "https://opencode.ai/zen/go/v1",
			APIKeyEntries: []management.APIKeyEntry{
				{APIKey: "op-key-1", AuthIndex: "opc-1"},
				{APIKey: "op-key-without-index"},
				{APIKey: "op-key-2", AuthIndex: " opc-2 "},
			},
			Models: []management.ModelAlias{{Name: "opencode-model"}},
		},
	)
	files, err := OpenCodeAuthFiles(context.Background(), client)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("files = %+v, want one target per indexed key", files)
	}
	for index, want := range []string{"opc-1", "opc-2"} {
		file := files[index]
		if file.AuthIndex != want || file.Provider != OpenCodeCompatibilityName ||
			file.Type != "openai-compatibility" || file.Name != "OpenCode" || file.AccountType != "api_key" || file.Disabled {
			t.Fatalf("file[%d] = %+v", index, file)
		}
	}
}

func TestOpenCodeAuthFilesResolvesLegacyKeysFromTheV8View(t *testing.T) {
	client := rosterCompatibilityClient(management.OpenAICompatibility{
		Name: "opencode", BaseURL: "https://opencode.ai/zen/go/v1",
		LegacyAPIKeys: []string{"legacy-key-1", "legacy-key-2"},
	})
	client.configView = func(context.Context) (map[string]any, management.ResponseMeta, error) {
		return map[string]any{
			"api-keys": map[string]any{
				"openai-compatibility": []any{
					map[string]any{"name": "other", "keys": []any{
						map[string]any{"api-key": "other-key", "auth-index": "other-index"},
					}},
					map[string]any{"name": "opencode", "keys": []any{
						map[string]any{"api-key": "legacy-key-1", "auth-index": "legacy-auth-1"},
						map[string]any{"api-key": "legacy-key-2", "auth_index": "legacy-auth-2"},
					}},
				},
			},
		}, management.ResponseMeta{}, nil
	}
	files, err := OpenCodeAuthFiles(context.Background(), client)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || files[0].AuthIndex != "legacy-auth-1" || files[1].AuthIndex != "legacy-auth-2" {
		t.Fatalf("files = %+v, want both legacy keys resolved from the v8 view", files)
	}
}

func TestOpenCodeAuthFilesLeavesUnresolvedLegacyKeysOut(t *testing.T) {
	client := rosterCompatibilityClient(management.OpenAICompatibility{
		Name: "opencode", BaseURL: "https://opencode.ai/zen/go/v1",
		LegacyAPIKeys: []string{"legacy-key-1", "legacy-key-2"},
	})
	// The stored group has only one key: positional pairing would attach the one
	// index to whichever legacy key happened to sit first, so neither is resolved.
	client.configView = func(context.Context) (map[string]any, management.ResponseMeta, error) {
		return map[string]any{
			"api-keys": map[string]any{"openai-compatibility": []any{
				map[string]any{"name": "opencode", "keys": []any{
					map[string]any{"api-key": "legacy-key-1", "auth-index": "legacy-auth-1"},
				}},
			}},
		}, management.ResponseMeta{}, nil
	}
	files, err := OpenCodeAuthFiles(context.Background(), client)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].AuthIndex != "legacy-auth-1" {
		t.Fatalf("files = %+v, want only the value-matched key", files)
	}
}

func TestOpenCodeAuthFilesRefusesAmbiguousOrUnusableEntries(t *testing.T) {
	for name, test := range map[string]struct {
		client    CPAClient
		wantFiles int
		wantErr   bool
	}{
		"two enabled entries": {
			client: rosterCompatibilityClient(
				management.OpenAICompatibility{Name: "opencode", APIKeyEntries: []management.APIKeyEntry{{AuthIndex: "a"}}},
				management.OpenAICompatibility{Name: "OpenCode", APIKeyEntries: []management.APIKeyEntry{{AuthIndex: "b"}}},
			),
			wantErr: true,
		},
		"only a disabled entry": {
			client: rosterCompatibilityClient(management.OpenAICompatibility{
				Name: "opencode", Disabled: true, APIKeyEntries: []management.APIKeyEntry{{AuthIndex: "a"}},
			}),
		},
		"renamed entry": {
			client: rosterCompatibilityClient(management.OpenAICompatibility{
				Name: "opencode-go", BaseURL: "https://opencode.ai/zen/go/v1",
				APIKeyEntries: []management.APIKeyEntry{{AuthIndex: "a"}},
			}),
		},
		"no compatibility listing": {
			client: &mockCPAClient{},
		},
		"compatibility read fails": {
			client: &rosterQuotaClient{compatibility: func(context.Context) (management.OpenAICompatibilityResponse, error) {
				return management.OpenAICompatibilityResponse{}, errors.New("gateway unavailable")
			}},
			wantErr: true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			files, err := OpenCodeAuthFiles(context.Background(), test.client)
			if test.wantErr != (err != nil) {
				t.Fatalf("err = %v, wantErr %t", err, test.wantErr)
			}
			if len(files) != test.wantFiles {
				t.Fatalf("files = %+v, want %d", files, test.wantFiles)
			}
		})
	}
}

func TestOpenCodeEntryKeyIndexesKeepsUnstatedIndexesEmpty(t *testing.T) {
	entry := management.OpenAICompatibility{
		Name: "opencode",
		APIKeyEntries: []management.APIKeyEntry{
			{AuthIndex: "first"}, {}, {AuthIndex: "third"},
		},
	}
	indexes, err := OpenCodeEntryKeyIndexes(context.Background(), &mockCPAClient{}, entry)
	if err != nil {
		t.Fatal(err)
	}
	if len(indexes) != 3 || indexes[0] != "first" || indexes[1] != "" || indexes[2] != "third" {
		t.Fatalf("indexes = %+v", indexes)
	}
}
