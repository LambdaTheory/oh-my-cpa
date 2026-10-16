package repository

import (
	"context"
	"testing"
	"time"
)

func TestFeishuSettingsAndSecretRoundTrip(t *testing.T) {
	repo, cipher := testRepository(t)
	ctx := context.Background()

	if _, found, err := repo.LoadFeishuSettings(ctx); err != nil || found {
		t.Fatalf("expected no settings row yet, found=%v err=%v", found, err)
	}
	settings := FeishuSettings{
		Enabled:                 true,
		AppID:                   "cli_x",
		ChatID:                  "oc_x",
		SummaryTimesJSON:        `["10:00","17:00"]`,
		Timezone:                "Asia/Shanghai",
		QuietStart:              "23:00",
		QuietEnd:                "08:00",
		TightRemainingPercent:   20,
		ChannelUnavailableAfter: 1800000,
		MonitorDownAfterMS:      1800000,
	}
	if err := repo.SaveFeishuSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	loaded, found, err := repo.LoadFeishuSettings(ctx)
	if err != nil || !found {
		t.Fatalf("settings not found: found=%v err=%v", found, err)
	}
	if loaded.AppID != "cli_x" || loaded.ChatID != "oc_x" || !loaded.Enabled {
		t.Fatalf("settings round trip mismatch: %+v", loaded)
	}

	ciphertext, nonce, err := cipher.Encrypt([]byte("app-secret-value"))
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveFeishuSecret(ctx, ciphertext, nonce); err != nil {
		t.Fatal(err)
	}
	gotCipher, gotNonce, has, err := repo.LoadFeishuSecret(ctx)
	if err != nil || !has {
		t.Fatalf("secret not found: has=%v err=%v", has, err)
	}
	plaintext, err := cipher.Decrypt(gotCipher, gotNonce)
	if err != nil {
		t.Fatal(err)
	}
	if string(plaintext) != "app-secret-value" {
		t.Fatalf("secret round trip = %q", plaintext)
	}
}

func TestFeishuRuntimeRoundTrip(t *testing.T) {
	repo, _ := testRepository(t)
	ctx := context.Background()
	nowMS := time.Now().UnixMilli()
	laterMS := nowMS + 60000

	runtime := FeishuRuntime{
		Bootstrapped:    true,
		LastSummaryAtMS: &nowMS,
		Pending: []FeishuPendingItem{{
			ID: "pending-1", Kind: "credential_invalid", Provider: "codex", CredKey: "a1",
			Alias: "work", SinceMS: &nowMS, LastSeenMS: &nowMS, Action: "reauth",
			Status: "open", NotifiedAtMS: &nowMS,
		}},
		Notifications: []FeishuNotification{{
			ID: "n1", Kind: "credential_invalid", ItemIDsJSON: `["pending-1"]`,
			AlertsJSON:  `[{"Kind":"credential_invalid","Provider":"codex"}]`,
			PendingJSON: "[]", TightJSON: "[]", State: "pending", Attempts: 2,
			NextAttemptAtMS: &laterMS, OccurredAtMS: &nowMS, CreatedAtMS: nowMS,
		}},
		Tracks: []FeishuChannelTrack{{Provider: "codex", FirstSeenMS: &nowMS, LastValidMS: &laterMS}},
	}
	if err := repo.SaveFeishuRuntime(ctx, runtime); err != nil {
		t.Fatal(err)
	}
	loaded, err := repo.LoadFeishuRuntime(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Bootstrapped || loaded.LastSummaryAtMS == nil || *loaded.LastSummaryAtMS != nowMS {
		t.Fatalf("runtime memory mismatch: %+v", loaded)
	}
	if len(loaded.Pending) != 1 || loaded.Pending[0].ID != "pending-1" || loaded.Pending[0].Status != "open" {
		t.Fatalf("pending mismatch: %+v", loaded.Pending)
	}
	if len(loaded.Notifications) != 1 || loaded.Notifications[0].Attempts != 2 {
		t.Fatalf("notifications mismatch: %+v", loaded.Notifications)
	}
	if len(loaded.Tracks) != 1 || loaded.Tracks[0].Provider != "codex" {
		t.Fatalf("tracks mismatch: %+v", loaded.Tracks)
	}

	// A second save replaces the lists rather than appending.
	runtime.Pending = nil
	runtime.Notifications = nil
	runtime.Tracks = nil
	if err := repo.SaveFeishuRuntime(ctx, runtime); err != nil {
		t.Fatal(err)
	}
	loaded, err = repo.LoadFeishuRuntime(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Pending) != 0 || len(loaded.Notifications) != 0 || len(loaded.Tracks) != 0 {
		t.Fatalf("runtime lists should be replaced, got %+v", loaded)
	}
}
