package repository

import (
	"context"
	"strings"
	"testing"
)

// TestOllamaAccountCookieIsEncryptedAtRest pins the core secret boundary: the
// cookie is stored as ciphertext, is absent from the metadata read, and is returned
// only to the collector, where it decrypts to the original.
func TestOllamaAccountCookieIsEncryptedAtRest(t *testing.T) {
	repo, cipher := testRepository(t)
	ctx := context.Background()
	const secret = "super-secret-session-cookie"
	ciphertext, nonce, err := cipher.Encrypt([]byte(secret))
	if err != nil {
		t.Fatal(err)
	}
	account, err := repo.CreateOllamaAccount(ctx, OllamaAccountCreate{
		Label: "work", Enabled: true, Ciphertext: ciphertext, Nonce: nonce,
	})
	if err != nil {
		t.Fatal(err)
	}
	if account.ID == "" {
		t.Fatal("a stable opaque id must be minted")
	}
	if !account.HasCookie {
		t.Fatal("has_cookie must be true")
	}

	var stored []byte
	if err := repo.SQL().QueryRowContext(ctx, `SELECT cookie_ciphertext FROM ollama_accounts WHERE id = ?`, account.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stored), secret) {
		t.Fatal("the cookie was stored in plaintext")
	}

	if _, err := repo.ListOllamaAccounts(ctx); err != nil {
		t.Fatalf("metadata list must not read the secret: %v", err)
	}

	secrets, err := repo.ListEnabledOllamaAccountSecrets(ctx)
	if err != nil || len(secrets) != 1 {
		t.Fatalf("secrets = %+v %v", secrets, err)
	}
	decrypted, err := cipher.Decrypt(secrets[0].Ciphertext, secrets[0].Nonce)
	if err != nil || string(decrypted) != secret {
		t.Fatalf("decrypt = %q %v", decrypted, err)
	}
}

func TestOllamaAccountIDIsNotDerivedFromInput(t *testing.T) {
	repo, cipher := testRepository(t)
	ctx := context.Background()
	ciphertext, nonce, _ := cipher.Encrypt([]byte("cookie"))
	account, err := repo.CreateOllamaAccount(ctx, OllamaAccountCreate{
		Label: "person@example.test", Enabled: true, Ciphertext: ciphertext, Nonce: nonce,
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(account.ID, "person") || strings.Contains(account.ID, "cookie") {
		t.Fatalf("id must be opaque: %q", account.ID)
	}
}

func TestUpdateOllamaAccountKeepsCookieWhenOmitted(t *testing.T) {
	repo, cipher := testRepository(t)
	ctx := context.Background()
	ciphertext, nonce, _ := cipher.Encrypt([]byte("original-cookie"))
	account, err := repo.CreateOllamaAccount(ctx, OllamaAccountCreate{Enabled: true, Ciphertext: ciphertext, Nonce: nonce})
	if err != nil {
		t.Fatal(err)
	}

	label := "renamed"
	disabled := false
	updated, found, err := repo.UpdateOllamaAccount(ctx, account.ID, OllamaAccountUpdate{Label: &label, Enabled: &disabled})
	if err != nil || !found {
		t.Fatalf("update = %v %v", found, err)
	}
	if updated.Label != "renamed" || updated.Enabled || !updated.HasCookie {
		t.Fatalf("update result = %+v", updated)
	}
	// A disabled account is not offered to the collector.
	if secrets, _ := repo.ListEnabledOllamaAccountSecrets(ctx); len(secrets) != 0 {
		t.Fatalf("a disabled account must not be collected: %+v", secrets)
	}

	enabled := true
	if _, _, err := repo.UpdateOllamaAccount(ctx, account.ID, OllamaAccountUpdate{Enabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	secrets, err := repo.ListEnabledOllamaAccountSecrets(ctx)
	if err != nil || len(secrets) != 1 {
		t.Fatalf("secrets = %+v %v", secrets, err)
	}
	decrypted, err := cipher.Decrypt(secrets[0].Ciphertext, secrets[0].Nonce)
	if err != nil || string(decrypted) != "original-cookie" {
		t.Fatalf("an omitted cookie must be preserved, got %q %v", decrypted, err)
	}
}

func TestUpdateOllamaAccountReplacesCookie(t *testing.T) {
	repo, cipher := testRepository(t)
	ctx := context.Background()
	first, nonce, _ := cipher.Encrypt([]byte("first-cookie"))
	account, _ := repo.CreateOllamaAccount(ctx, OllamaAccountCreate{Enabled: true, Ciphertext: first, Nonce: nonce})

	second, secondNonce, _ := cipher.Encrypt([]byte("second-cookie"))
	if _, _, err := repo.UpdateOllamaAccount(ctx, account.ID, OllamaAccountUpdate{SetCookie: true, Ciphertext: second, Nonce: secondNonce}); err != nil {
		t.Fatal(err)
	}
	secrets, _ := repo.ListEnabledOllamaAccountSecrets(ctx)
	decrypted, err := cipher.Decrypt(secrets[0].Ciphertext, secrets[0].Nonce)
	if err != nil || string(decrypted) != "second-cookie" {
		t.Fatalf("cookie replacement = %q %v", decrypted, err)
	}
}

func TestUpdateOllamaAccountMissing(t *testing.T) {
	repo, _ := testRepository(t)
	_, found, err := repo.UpdateOllamaAccount(context.Background(), "does-not-exist", OllamaAccountUpdate{})
	if err != nil || found {
		t.Fatalf("update missing = found %v err %v", found, err)
	}
}

func TestDeleteOllamaAccount(t *testing.T) {
	repo, cipher := testRepository(t)
	ctx := context.Background()
	ciphertext, nonce, _ := cipher.Encrypt([]byte("cookie"))
	account, _ := repo.CreateOllamaAccount(ctx, OllamaAccountCreate{Enabled: true, Ciphertext: ciphertext, Nonce: nonce})

	removed, err := repo.DeleteOllamaAccount(ctx, account.ID)
	if err != nil || !removed {
		t.Fatalf("delete = %v %v", removed, err)
	}
	if count, _ := repo.CountOllamaAccounts(ctx); count != 0 {
		t.Fatalf("count after delete = %d", count)
	}
	if removed, _ := repo.DeleteOllamaAccount(ctx, account.ID); removed {
		t.Fatal("a second delete must report not found")
	}
}

// TestRecordOllamaResultKeepsTheLastGoodSnapshot pins that a later failure does not
// erase the last successful reading.
func TestRecordOllamaResultKeepsTheLastGoodSnapshot(t *testing.T) {
	repo, cipher := testRepository(t)
	ctx := context.Background()
	ciphertext, nonce, _ := cipher.Encrypt([]byte("cookie"))
	account, _ := repo.CreateOllamaAccount(ctx, OllamaAccountCreate{Enabled: true, Ciphertext: ciphertext, Nonce: nonce})

	snapshot := `{"auth_index":"ollama:` + account.ID + `","provider":"ollama","windows":[]}`
	recorded, found, err := repo.RecordOllamaResult(ctx, OllamaCollectionResult{
		ID: account.ID, Status: "ok", ObservedAtMS: 5000, IsSuccess: true, SnapshotJSON: snapshot,
	})
	if err != nil || !found {
		t.Fatalf("record success = %v %v", found, err)
	}
	if recorded.Status != "ok" || recorded.LastSuccessAtMS == nil || *recorded.LastSuccessAtMS != 5000 {
		t.Fatalf("recorded = %+v", recorded)
	}
	if recorded.QuotaSnapshotJSON != snapshot || recorded.QuotaObservedAtMS == nil || *recorded.QuotaObservedAtMS != 5000 {
		t.Fatalf("snapshot not stored: %+v", recorded)
	}

	recorded, _, err = repo.RecordOllamaResult(ctx, OllamaCollectionResult{
		ID: account.ID, Status: "transport_error", ObservedAtMS: 6000, Error: "network down", ErrorAtMS: 6000, AttemptCount: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if recorded.QuotaSnapshotJSON != snapshot {
		t.Fatalf("a failure erased the last snapshot: %q", recorded.QuotaSnapshotJSON)
	}
	if recorded.LastSuccessAtMS == nil || *recorded.LastSuccessAtMS != 5000 {
		t.Fatalf("last success moved on failure: %+v", recorded.LastSuccessAtMS)
	}
	if recorded.NewestError != "network down" || recorded.NewestErrorAtMS == nil || *recorded.NewestErrorAtMS != 6000 {
		t.Fatalf("error not recorded: %+v", recorded)
	}
	if recorded.LastObservedAtMS == nil || *recorded.LastObservedAtMS != 6000 {
		t.Fatalf("observed instant not advanced: %+v", recorded.LastObservedAtMS)
	}
}

// TestOllamaAccountsAreNotReadableByOperatorQueries records that an unclassified
// table is refused by the raw SQL path by default, so the encrypted cookie cannot be
// read even though the table is not yet declared hidden.
func TestOllamaAccountsAreNotReadableByOperatorQueries(t *testing.T) {
	repo := queryTestRepository(t)
	ctx := context.Background()
	if _, err := repo.CreateOllamaAccount(ctx, OllamaAccountCreate{
		Label: "work", Enabled: true, Ciphertext: []byte("cipher"), Nonce: []byte("nonce"),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ReadOnlyQuery(ctx, `SELECT id FROM ollama_accounts`, 10); err == nil {
		t.Fatal("an unclassified table must not be readable through the operator query path")
	}
	if _, err := repo.ReadOnlyQuery(ctx, `SELECT cookie_ciphertext FROM ollama_accounts`, 10); err == nil {
		t.Fatal("the encrypted cookie must never be readable through the operator query path")
	}
}
