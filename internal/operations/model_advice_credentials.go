package operations

import (
	"context"
	"fmt"
	"strings"

	"github.com/oh-my-cpa/oh-my-cpa/internal/cpa/management"
	"github.com/oh-my-cpa/oh-my-cpa/internal/quota"
)

func (s *Service) ListAdviceCredentials(ctx context.Context) ([]AdviceCredential, error) {
	client, err := s.Client(ctx)
	if err != nil {
		return nil, err
	}
	files, err := client.AuthFiles(ctx)
	if err != nil {
		return nil, err
	}
	modelNames := func(models []management.ModelAlias) []string {
		names := make([]string, 0, len(models))
		for _, model := range models {
			if model.Name != "" {
				names = append(names, model.Name)
			}
		}
		return names
	}
	byIndex := map[string]AdviceCredential{}
	items := []AdviceCredential{}
	addConfigured := func(credential AdviceCredential) {
		if credential.AuthIndex == "" {
			items = append(items, credential)
		} else {
			byIndex[credential.AuthIndex] = credential
		}
	}
	for _, family := range management.ConfigKeyFamilies {
		entries, readErr := client.ConfigAPIKeys(ctx, family)
		if readErr != nil {
			continue
		}
		for index, entry := range entries {
			if entry.APIKey == "" {
				continue
			}
			addConfigured(AdviceCredential{
				AuthIndex: entry.AuthIndex, Provider: string(family),
				ProviderID: fmt.Sprintf("%s-%d", family, index), ExcludedModels: entry.ExcludedModels,
				IsDisabled: management.IsExcludedAll(entry.ExcludedModels), Models: modelNames(entry.Models),
			})
		}
	}
	if compatibility, readErr := client.OpenAICompatibility(ctx); readErr == nil {
		for index, entry := range compatibility.Entries {
			keys := entry.APIKeyEntries
			if len(keys) == 0 {
				for range entry.LegacyAPIKeys {
					keys = append(keys, management.APIKeyEntry{})
				}
			}
			// The opencode entry is read by its own built-in reader, which stores
			// the reading under provider "opencode". The credential must carry the
			// same key or advice cannot pair the model with its window. The
			// provider id stays the entry's position, which is what the directory's
			// routes use. A legacy key has no auth index in the v0 listing, so it
			// is resolved the same way the refresh roster resolves it.
			provider := "openai-compatibility"
			var resolvedIndexes []string
			if strings.EqualFold(strings.TrimSpace(entry.Name), quota.OpenCodeCompatibilityName) {
				provider = quota.OpenCodeCompatibilityName
				resolvedIndexes, _ = quota.OpenCodeEntryKeyIndexes(ctx, client, entry)
			}
			for position, key := range keys {
				authIndex := strings.TrimSpace(key.AuthIndex)
				if authIndex == "" && position < len(resolvedIndexes) {
					authIndex = resolvedIndexes[position]
				}
				addConfigured(AdviceCredential{
					AuthIndex: authIndex, Provider: provider,
					ProviderID: fmt.Sprintf("openai-compat-%d", index), IsDisabled: entry.Disabled,
					Models: modelNames(entry.Models),
				})
			}
		}
	}
	for _, file := range files.Files {
		item := byIndex[file.AuthIndex]
		delete(byIndex, file.AuthIndex)
		item.AuthIndex = file.AuthIndex
		if item.Provider == "" {
			item.Provider = quota.DetectProvider(file.Type, file.Provider)
		}
		item.IsDisabled = item.IsDisabled || file.Disabled
		if len(item.Models) == 0 {
			known := file.Models
			if len(known) == 0 && file.Name != "" && !item.IsDisabled {
				if resolved, readErr := client.AuthFileModels(ctx, file.Name); readErr == nil {
					known = resolved
				}
			}
			for _, model := range known {
				item.Models = append(item.Models, model.ID)
			}
		}
		items = append(items, item)
	}
	for _, item := range byIndex {
		items = append(items, item)
	}
	return items, nil
}
