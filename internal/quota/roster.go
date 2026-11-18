package quota

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/oh-my-cpa/oh-my-cpa/internal/cpa/management"
)

// configViewReader is the v8 configuration read the legacy-key fallback needs.
// *management.Client implements it; keeping it separate means a bare CPAClient
// double does not have to grow the method.
type configViewReader interface {
	ConfigView(context.Context) (map[string]any, management.ResponseMeta, error)
}

// OpenCodeAuthFiles builds the refresh targets for the keys of the opencode
// openai-compatibility entry.
//
// CPA lists that entry's keys only under openai-compatibility, not among the
// auth files, so the quota readers have no credential to name without this
// translation. Each key becomes an AuthFile carrying its auth index, with the
// provider set to the built-in reader's key (opencode). The entry is selected
// exactly as the reader selects it — name compared case-insensitively after
// trimming, one enabled match — so a renamed, duplicated or disabled entry
// yields no targets instead of a guess. A key whose auth index cannot be
// resolved is left out: a request carrying an empty index would go to CPA with
// no credential.
func OpenCodeAuthFiles(ctx context.Context, client CPAClient) ([]management.AuthFile, error) {
	entry, found, err := openCodeCompatibilityEntry(ctx, client)
	if err != nil || !found {
		return nil, err
	}
	indexes, err := OpenCodeEntryKeyIndexes(ctx, client, entry)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(entry.Name)
	files := make([]management.AuthFile, 0, len(indexes))
	for _, authIndex := range indexes {
		if authIndex == "" {
			continue
		}
		files = append(files, management.AuthFile{
			AuthIndex:   authIndex,
			Name:        name,
			Type:        "openai-compatibility",
			Provider:    OpenCodeCompatibilityName,
			AccountType: "api_key",
		})
	}
	return files, nil
}

// OpenCodeEntryKeyIndexes resolves one compatibility entry's key auth indexes in
// the entry's own key order, with "" for a key whose index cannot be resolved.
//
// The v0 listing carries each api-key-entries[] key's auth index. The pre-v8
// `api-keys` spelling carries plain strings, so the index is read from the v8
// configuration view, where CPA keeps the runtime-injected index beside each
// key. Keys are matched to that view by their api-key value first and by
// position only when the two lists have the same length; anything else stays
// unresolved rather than risking another credential's index.
func OpenCodeEntryKeyIndexes(ctx context.Context, client CPAClient, entry management.OpenAICompatibility) ([]string, error) {
	keys := entry.APIKeyEntries
	if len(keys) > 0 {
		indexes := make([]string, len(keys))
		for index, key := range keys {
			indexes[index] = strings.TrimSpace(key.AuthIndex)
		}
		return indexes, nil
	}
	legacy := entry.LegacyAPIKeys
	if len(legacy) == 0 {
		return nil, nil
	}
	indexes := make([]string, len(legacy))
	reader, ok := client.(configViewReader)
	if client == nil || !ok {
		return indexes, nil
	}
	view, _, err := reader.ConfigView(ctx)
	if err != nil {
		return nil, fmt.Errorf("read v8 configuration for %q key auth indexes: %w", OpenCodeCompatibilityName, err)
	}
	group := openCodeCompatibilityGroup(view, entry.Name)
	if group == nil {
		return indexes, nil
	}
	byAPIKey := map[string]string{}
	ordered := make([]string, 0, len(legacy))
	rawKeys, _ := group["keys"].([]any)
	for _, rawKey := range rawKeys {
		key, ok := rawKey.(map[string]any)
		if !ok {
			continue
		}
		authIndex := configKeyAuthIndex(key)
		ordered = append(ordered, authIndex)
		if apiKey, _ := key["api-key"].(string); strings.TrimSpace(apiKey) != "" {
			byAPIKey[strings.TrimSpace(apiKey)] = authIndex
		}
	}
	canPairByPosition := len(ordered) == len(legacy)
	for index, apiKey := range legacy {
		if authIndex := strings.TrimSpace(byAPIKey[strings.TrimSpace(apiKey)]); authIndex != "" {
			indexes[index] = authIndex
			continue
		}
		if canPairByPosition {
			indexes[index] = strings.TrimSpace(ordered[index])
		}
	}
	return indexes, nil
}

// configKeyAuthIndex reads the runtime auth index of one stored key. CPA's v8
// view spells it `auth-index`; the underscore spelling is accepted too so a
// host that renders the field its own way is not silently unresolved.
func configKeyAuthIndex(key map[string]any) string {
	for _, field := range []string{"auth-index", "auth_index"} {
		if value, ok := key[field].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// openCodeCompatibilityGroup finds the stored v8 group by the entry's name.
func openCodeCompatibilityGroup(view map[string]any, name string) map[string]any {
	value, ok := management.ValueAt(view, management.OPENAI_COMPATIBILITY_GROUPS_PATH)
	if !ok {
		return nil
	}
	groups, _ := value.([]any)
	for _, rawGroup := range groups {
		group, ok := rawGroup.(map[string]any)
		if !ok {
			continue
		}
		groupName, _ := group["name"].(string)
		if strings.EqualFold(strings.TrimSpace(groupName), strings.TrimSpace(name)) {
			return group
		}
	}
	return nil
}

// openCodeCompatibilityEntry selects the entry whose keys OpenCode's own usage
// document describes. Only the exact name is eligible; the same rule the reader
// applies, so the roster and the reader never disagree about which entry serves
// a credential.
func openCodeCompatibilityEntry(ctx context.Context, client CPAClient) (management.OpenAICompatibility, bool, error) {
	if client == nil {
		return management.OpenAICompatibility{}, false, errors.New("CPA client is not configured")
	}
	reader, ok := client.(openAICompatibilityReader)
	if !ok {
		return management.OpenAICompatibility{}, false, nil
	}
	response, err := reader.OpenAICompatibility(ctx)
	if err != nil {
		return management.OpenAICompatibility{}, false, fmt.Errorf("read openai-compatibility entries: %w", err)
	}
	matches := make([]management.OpenAICompatibility, 0, 1)
	for _, entry := range response.Entries {
		if strings.EqualFold(strings.TrimSpace(entry.Name), OpenCodeCompatibilityName) {
			matches = append(matches, entry)
		}
	}
	enabled := make([]management.OpenAICompatibility, 0, len(matches))
	for _, entry := range matches {
		if !entry.Disabled {
			enabled = append(enabled, entry)
		}
	}
	switch {
	case len(enabled) == 1:
		return enabled[0], true, nil
	case len(enabled) > 1:
		return management.OpenAICompatibility{}, false, fmt.Errorf("%d enabled %q openai-compatibility entries; refusing to guess which one serves this credential", len(enabled), OpenCodeCompatibilityName)
	case len(matches) > 1:
		return management.OpenAICompatibility{}, false, fmt.Errorf("%d disabled %q openai-compatibility entries; refusing to guess which one serves this credential", len(matches), OpenCodeCompatibilityName)
	default:
		// No candidate, or only one disabled entry: nothing to refresh.
		return management.OpenAICompatibility{}, false, nil
	}
}
