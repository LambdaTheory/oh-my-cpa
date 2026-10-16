package repository

import (
	"context"
	"testing"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/domain"
)

// TestListFeishuCredentialMetadataReadsStoredFlags pins that the report's
// credential dimension is read from the stored discovery metadata: the cached
// disabled/unavailable flags and the last-sync instant, never the live gateway.
func TestListFeishuCredentialMetadataReadsStoredFlags(t *testing.T) {
	repo, _ := testRepository(t)
	ctx := context.Background()
	if err := repo.UpsertInstance(ctx, domain.CPAInstance{
		ID: "meta-test", Name: "Meta", BaseURL: "http://cpa",
		ManagementKeyCiphertext: []byte("cipher"), ManagementKeyNonce: []byte("nonce"),
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	synced := time.Date(2026, 10, 15, 3, 0, 0, 0, time.UTC)
	if _, err := repo.UpsertDiscoveredResources(ctx, "meta-test", []domain.DiscoveredResource{
		{
			ResourceKey: "auth-index:auth-file:idx-on", CPAResourceType: "auth-file",
			CPAAuthIndex: "idx-on", CPAResourceName: "on.json", CPADriver: "codex",
			Status: domain.ResourceStatusUnclaimed,
		},
		{
			ResourceKey: "auth-index:auth-file:idx-off", CPAResourceType: "auth-file",
			CPAAuthIndex: "idx-off", CPAResourceName: "off.json", CPADriver: "claude",
			Status:  domain.ResourceStatusUnclaimed,
			Details: domain.ResourceDetails{Disabled: true, Unavailable: true},
		},
		{
			// A non-auth-file resource is not a credential the report names.
			ResourceKey: "auth-index:codex-api-key:idx-key", CPAResourceType: "codex-api-key",
			CPAAuthIndex: "idx-key", CPADriver: "codex", Status: domain.ResourceStatusUnclaimed,
		},
	}, synced, false); err != nil {
		t.Fatal(err)
	}

	metadata, err := repo.ListFeishuCredentialMetadata(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byIndex := map[string]FeishuCredentialMetadata{}
	for _, entry := range metadata {
		byIndex[entry.AuthIndex] = entry
	}
	on, ok := byIndex["idx-on"]
	if !ok || on.Disabled || on.Unavailable || on.Name != "on.json" || on.Driver != "codex" {
		t.Fatalf("idx-on = %+v present=%v", on, ok)
	}
	if !on.ObservedAt.Equal(synced) {
		t.Fatalf("idx-on metadata age = %s, want the stored sync %s", on.ObservedAt, synced)
	}
	off, ok := byIndex["idx-off"]
	if !ok || !off.Disabled || !off.Unavailable {
		t.Fatalf("idx-off = %+v present=%v", off, ok)
	}
	if _, ok := byIndex["idx-key"]; ok {
		t.Fatal("a non-auth-file resource must not be reported as a credential")
	}
}

// TestListFeishuCredentialMetadataExcludesMissing pins that a credential CPA has
// removed is not reported as still configured: only a complete discovery sweep
// marks it missing, and the report respects that.
func TestListFeishuCredentialMetadataExcludesMissing(t *testing.T) {
	repo, _ := testRepository(t)
	ctx := context.Background()
	if err := repo.UpsertInstance(ctx, domain.CPAInstance{
		ID: "missing-test", Name: "Missing", BaseURL: "http://cpa",
		ManagementKeyCiphertext: []byte("cipher"), ManagementKeyNonce: []byte("nonce"),
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.UpsertDiscoveredResources(ctx, "missing-test", []domain.DiscoveredResource{
		{ResourceKey: "auth-index:auth-file:stay", CPAResourceType: "auth-file", CPAAuthIndex: "stay", CPADriver: "codex", Status: domain.ResourceStatusUnclaimed},
		{ResourceKey: "auth-index:auth-file:gone", CPAResourceType: "auth-file", CPAAuthIndex: "gone", CPADriver: "codex", Status: domain.ResourceStatusUnclaimed},
	}, time.Now().UTC(), false); err != nil {
		t.Fatal(err)
	}
	// A complete sweep that no longer sees "gone" marks it missing.
	if _, err := repo.UpsertDiscoveredResources(ctx, "missing-test", []domain.DiscoveredResource{
		{ResourceKey: "auth-index:auth-file:stay", CPAResourceType: "auth-file", CPAAuthIndex: "stay", CPADriver: "codex", Status: domain.ResourceStatusUnclaimed},
	}, time.Now().UTC(), true); err != nil {
		t.Fatal(err)
	}

	metadata, err := repo.ListFeishuCredentialMetadata(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range metadata {
		if entry.AuthIndex == "gone" {
			t.Fatalf("a missing credential must not be reported: %+v", entry)
		}
	}
}

// TestListKnownQuotaProvidersNamesPersistedChannels pins the fallback dimension:
// every provider the observation timeline has recorded is named once, sorted.
// TestFeishuAccountSignalUpsertAndAuthPreservation pins the signal store: one row
// per credential upserted in place, and a transport failure never erases a known
// auth failure, so the report never mistakes a lost auth failure for recovery.
func TestFeishuAccountSignalUpsertAndAuthPreservation(t *testing.T) {
	repo, _ := testRepository(t)
	ctx := context.Background()
	observed := time.Date(2026, 10, 15, 3, 0, 0, 0, time.UTC)
	load := func(authIndex string) FeishuAccountSignal {
		t.Helper()
		signals, err := repo.GetFeishuAccountSignals(ctx, []string{authIndex})
		if err != nil {
			t.Fatal(err)
		}
		return signals[authIndex]
	}

	if err := repo.SaveFeishuAccountSignal(ctx, FeishuAccountSignal{AuthIndex: "sig-1", Provider: "codex", State: FeishuSignalHealthy, ObservedAt: observed}); err != nil {
		t.Fatal(err)
	}
	if got := load("sig-1"); got.State != FeishuSignalHealthy || !got.ObservedAt.Equal(observed) {
		t.Fatalf("healthy signal = %+v", got)
	}

	later := observed.Add(10 * time.Minute)
	if err := repo.SaveFeishuAccountSignal(ctx, FeishuAccountSignal{AuthIndex: "sig-1", Provider: "codex", State: FeishuSignalAuthInvalid, ObservedAt: later}); err != nil {
		t.Fatal(err)
	}
	if got := load("sig-1"); got.State != FeishuSignalAuthInvalid || !got.ObservedAt.Equal(later) {
		t.Fatalf("auth signal = %+v", got)
	}

	if err := repo.SaveFeishuAccountSignal(ctx, FeishuAccountSignal{AuthIndex: "sig-1", Provider: "codex", State: FeishuSignalTransport, ObservedAt: later.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if got := load("sig-1"); got.State != FeishuSignalAuthInvalid || !got.ObservedAt.Equal(later) {
		t.Fatalf("a transport failure must not erase the auth signal, got %+v", got)
	}

	recoveredAt := later.Add(2 * time.Minute)
	if err := repo.SaveFeishuAccountSignal(ctx, FeishuAccountSignal{AuthIndex: "sig-1", Provider: "codex", State: FeishuSignalHealthy, ObservedAt: recoveredAt}); err != nil {
		t.Fatal(err)
	}
	if got := load("sig-1"); got.State != FeishuSignalHealthy || !got.ObservedAt.Equal(recoveredAt) {
		t.Fatalf("a success must replace the auth signal, got %+v", got)
	}

	// An unrecognized state is coerced into the fixed vocabulary.
	if err := repo.SaveFeishuAccountSignal(ctx, FeishuAccountSignal{AuthIndex: "sig-2", State: "bogus"}); err != nil {
		t.Fatal(err)
	}
	if got := load("sig-2"); got.State != FeishuSignalUnknown {
		t.Fatalf("an unknown state must be coerced, got %+v", got)
	}
}

func TestListKnownQuotaProvidersNamesPersistedChannels(t *testing.T) {
	repo, _ := testRepository(t)
	ctx := context.Background()
	for _, insert := range []struct {
		provider string
		auth     string
	}{
		{"codex", "known-1"},
		{"claude", "known-2"},
		{"codex", "known-3"},
	} {
		if _, err := repo.SQL().ExecContext(ctx, `
			INSERT INTO quota_observations (
				auth_index, provider, window_kind, scope, model, window_minutes, used_percent,
				reset_at_ms, status, observed_at_ms, event_key, created_at_ms, source
			) VALUES (?, ?, 'weekly', 'standard', '', 10080, 10, NULL, '', 1, ?, 1, 'active')`,
			insert.auth, insert.provider, insert.auth); err != nil {
			t.Fatal(err)
		}
	}

	providers, err := repo.ListKnownQuotaProviders(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var seenCodex, seenClaude bool
	for i, provider := range providers {
		if i > 0 && providers[i-1] > provider {
			t.Fatalf("providers not sorted: %v", providers)
		}
		switch provider {
		case "codex":
			seenCodex = true
		case "claude":
			seenClaude = true
		}
	}
	if !seenCodex || !seenClaude {
		t.Fatalf("known providers = %v, want codex and claude", providers)
	}
	// Duplicate providers must collapse to one name.
	codexCount := 0
	for _, provider := range providers {
		if provider == "codex" {
			codexCount++
		}
	}
	if codexCount != 1 {
		t.Fatalf("codex must be named once, got %d in %v", codexCount, providers)
	}
}
