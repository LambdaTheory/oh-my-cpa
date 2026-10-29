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

// TestGetOllamaAccountSecretReadsDisabledAccount pins that the per-account reload
// used by the refresh paths finds a paused account too, so a one-off manual refresh
// can read its cookie even though periodic collection skips it.
func TestGetOllamaAccountSecretReadsDisabledAccount(t *testing.T) {
	repo, cipher := testRepository(t)
	ctx := context.Background()
	ciphertext, nonce, _ := cipher.Encrypt([]byte("paused-cookie"))
	account, err := repo.CreateOllamaAccount(ctx, OllamaAccountCreate{
		Label: "paused", Enabled: false, Ciphertext: ciphertext, Nonce: nonce, NowMS: 4242,
	})
	if err != nil {
		t.Fatal(err)
	}
	secret, found, err := repo.GetOllamaAccountSecret(ctx, account.ID)
	if err != nil || !found {
		t.Fatalf("secret read = %v %v", found, err)
	}
	if secret.Enabled {
		t.Fatal("the account is disabled, so enabled must be false")
	}
	if secret.CookieUpdatedAtMS != 4242 {
		t.Fatalf("cookie version = %d, want 4242", secret.CookieUpdatedAtMS)
	}
	if decrypted, derr := cipher.Decrypt(secret.Ciphertext, secret.Nonce); derr != nil || string(decrypted) != "paused-cookie" {
		t.Fatalf("decrypt = %q %v", decrypted, derr)
	}
	if _, found, err := repo.GetOllamaAccountSecret(ctx, "missing"); err != nil || found {
		t.Fatalf("missing read = %v %v", found, err)
	}
}

// TestRecordOllamaResultDroppedWhenCookieChanged pins the stale-cookie guard: a
// scrape that read an old cookie cannot overwrite the reading of the cookie the
// operator has since replaced. A dropped write leaves the account exactly as it was.
func TestRecordOllamaResultDroppedWhenCookieChanged(t *testing.T) {
	repo, cipher := testRepository(t)
	ctx := context.Background()
	first, firstNonce, _ := cipher.Encrypt([]byte("first-cookie"))
	account, err := repo.CreateOllamaAccount(ctx, OllamaAccountCreate{
		Enabled: true, Ciphertext: first, Nonce: firstNonce, NowMS: 1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	secret, found, err := repo.GetOllamaAccountSecret(ctx, account.ID)
	if err != nil || !found {
		t.Fatalf("secret = %v %v", found, err)
	}
	oldVersion := secret.CookieUpdatedAtMS

	// The operator replaces the cookie; the account now carries a newer version.
	second, secondNonce, _ := cipher.Encrypt([]byte("second-cookie"))
	updated, found, err := repo.UpdateOllamaAccount(ctx, account.ID, OllamaAccountUpdate{
		SetCookie: true, Ciphertext: second, Nonce: secondNonce, NowMS: 2000,
	})
	if err != nil || !found {
		t.Fatalf("update = %v %v", found, err)
	}
	replaced, found, err := repo.GetOllamaAccountSecret(ctx, account.ID)
	if err != nil || !found {
		t.Fatalf("replaced secret = %v %v", found, err)
	}
	if replaced.CookieUpdatedAtMS == oldVersion {
		t.Fatal("the test needs two distinct cookie versions")
	}

	// A stale success carrying the old cookie version must be dropped entirely.
	stale := `{"auth_index":"ollama:` + account.ID + `","provider":"ollama","windows":[]}`
	after, found, err := repo.RecordOllamaResult(ctx, OllamaCollectionResult{
		ID: account.ID, Status: "ok", ObservedAtMS: 3000, IsSuccess: true, SnapshotJSON: stale,
		CookieUpdatedAtMS: &oldVersion,
	})
	if err != nil || !found {
		t.Fatalf("stale record = %v %v", found, err)
	}
	if after.Status != updated.Status || after.QuotaSnapshotJSON != "" || after.LastSuccessAtMS != nil {
		t.Fatalf("a stale cookie version overwrote the account: %+v", after)
	}

	// The current cookie version is applied.
	newVersion := replaced.CookieUpdatedAtMS
	applied, found, err := repo.RecordOllamaResult(ctx, OllamaCollectionResult{
		ID: account.ID, Status: "ok", ObservedAtMS: 4000, IsSuccess: true, SnapshotJSON: stale,
		CookieUpdatedAtMS: &newVersion,
	})
	if err != nil || !found {
		t.Fatalf("fresh record = %v %v", found, err)
	}
	if applied.Status != "ok" || applied.QuotaSnapshotJSON != stale || applied.LastSuccessAtMS == nil || *applied.LastSuccessAtMS != 4000 {
		t.Fatalf("the current cookie version was not applied: %+v", applied)
	}
}

// TestRecordOllamaResultForDeletedAccountIsANoOp pins that a scrape which finished
// after the account was deleted is dropped rather than resurrecting a row or
// failing.
func TestRecordOllamaResultForDeletedAccountIsANoOp(t *testing.T) {
	repo, cipher := testRepository(t)
	ctx := context.Background()
	ciphertext, nonce, _ := cipher.Encrypt([]byte("cookie"))
	account, _ := repo.CreateOllamaAccount(ctx, OllamaAccountCreate{Enabled: true, Ciphertext: ciphertext, Nonce: nonce})
	if removed, err := repo.DeleteOllamaAccount(ctx, account.ID); err != nil || !removed {
		t.Fatalf("delete = %v %v", removed, err)
	}
	_, found, err := repo.RecordOllamaResult(ctx, OllamaCollectionResult{
		ID: account.ID, Status: "ok", ObservedAtMS: 5000, IsSuccess: true,
		SnapshotJSON: `{"auth_index":"ollama:` + account.ID + `"}`,
	})
	if err != nil {
		t.Fatalf("recording a deleted account must not fail: %v", err)
	}
	if found {
		t.Fatal("a deleted account must not be reported as found")
	}
	if count, _ := repo.CountOllamaAccounts(ctx); count != 0 {
		t.Fatalf("a deleted account was resurrected: count = %d", count)
	}
}

// TestCookieVersionAdvancesStrictlyOnReplacement pins that replacing a cookie always
// raises its version, even when two writes share a millisecond. That is the one
// generation this lane still tracks: it reliably tells a result that read an old
// cookie apart from the new one.
func TestCookieVersionAdvancesStrictlyOnReplacement(t *testing.T) {
	repo, cipher := testRepository(t)
	ctx := context.Background()
	ciphertext, nonce, _ := cipher.Encrypt([]byte("cookie"))
	account, err := repo.CreateOllamaAccount(ctx, OllamaAccountCreate{
		Enabled: true, Ciphertext: ciphertext, Nonce: nonce, NowMS: 1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	created, _, _ := repo.GetOllamaAccountSecret(ctx, account.ID)

	second, secondNonce, _ := cipher.Encrypt([]byte("second"))
	if _, _, err := repo.UpdateOllamaAccount(ctx, account.ID, OllamaAccountUpdate{
		SetCookie: true, Ciphertext: second, Nonce: secondNonce, NowMS: 1000,
	}); err != nil {
		t.Fatal(err)
	}
	afterFirst, _, _ := repo.GetOllamaAccountSecret(ctx, account.ID)
	if afterFirst.CookieUpdatedAtMS <= created.CookieUpdatedAtMS {
		t.Fatalf("cookie version did not advance: %d -> %d", created.CookieUpdatedAtMS, afterFirst.CookieUpdatedAtMS)
	}

	third, thirdNonce, _ := cipher.Encrypt([]byte("third"))
	if _, _, err := repo.UpdateOllamaAccount(ctx, account.ID, OllamaAccountUpdate{
		SetCookie: true, Ciphertext: third, Nonce: thirdNonce, NowMS: 1000,
	}); err != nil {
		t.Fatal(err)
	}
	afterSecond, _, _ := repo.GetOllamaAccountSecret(ctx, account.ID)
	if afterSecond.CookieUpdatedAtMS <= afterFirst.CookieUpdatedAtMS {
		t.Fatalf("cookie version did not advance again: %d -> %d", afterFirst.CookieUpdatedAtMS, afterSecond.CookieUpdatedAtMS)
	}
}

// TestLabelEditDoesNotInvalidateReading pins the accepted policy: a label edit is
// ordinary metadata and does not carry a generation, so a reading that was in flight
// or already due is still written.
func TestLabelEditDoesNotInvalidateReading(t *testing.T) {
	repo, cipher := testRepository(t)
	ctx := context.Background()
	ciphertext, nonce, _ := cipher.Encrypt([]byte("cookie"))
	account, err := repo.CreateOllamaAccount(ctx, OllamaAccountCreate{
		Enabled: true, Ciphertext: ciphertext, Nonce: nonce, NowMS: 1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	secret, _, _ := repo.GetOllamaAccountSecret(ctx, account.ID)
	label := "renamed"
	if _, _, err := repo.UpdateOllamaAccount(ctx, account.ID, OllamaAccountUpdate{Label: &label, NowMS: 2000}); err != nil {
		t.Fatal(err)
	}
	recorded, found, err := repo.RecordOllamaResult(ctx, OllamaCollectionResult{
		ID: account.ID, Status: "ok", ObservedAtMS: 5000, IsSuccess: true,
		SnapshotJSON:      `{"auth_index":"ollama:` + account.ID + `","provider":"ollama","windows":[]}`,
		CookieUpdatedAtMS: &secret.CookieUpdatedAtMS, Auto: true,
	})
	if err != nil || !found {
		t.Fatalf("record = %v %v", found, err)
	}
	if recorded.Status != "ok" || recorded.Label != "renamed" {
		t.Fatalf("a label edit invalidated a reading: %+v", recorded)
	}
}

// TestRecordOllamaResultRejectsAutomaticWriteWhileDisabled pins the direct case: an
// automatic result never writes to a disabled account, while a manual refresh of the
// paused account does.
func TestRecordOllamaResultRejectsAutomaticWriteWhileDisabled(t *testing.T) {
	repo, cipher := testRepository(t)
	ctx := context.Background()
	ciphertext, nonce, _ := cipher.Encrypt([]byte("cookie"))
	account, err := repo.CreateOllamaAccount(ctx, OllamaAccountCreate{
		Enabled: true, Ciphertext: ciphertext, Nonce: nonce, NowMS: 1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	disabled := false
	if _, _, err := repo.UpdateOllamaAccount(ctx, account.ID, OllamaAccountUpdate{Enabled: &disabled, NowMS: 2000}); err != nil {
		t.Fatal(err)
	}
	paused, _, _ := repo.GetOllamaAccountSecret(ctx, account.ID)

	// An automatic result is refused while the account is disabled.
	after, _, err := repo.RecordOllamaResult(ctx, OllamaCollectionResult{
		ID: account.ID, Status: "transport_error", ObservedAtMS: 5000, Error: "rate limited",
		NextAttemptAtMS: int64Ptr(9999999), AttemptCount: 1, Auto: true,
		CookieUpdatedAtMS: &paused.CookieUpdatedAtMS,
	})
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != "" || after.NextAttemptAtMS != nil || after.AttemptCount != 0 {
		t.Fatalf("an automatic result wrote to a disabled account: %+v", after)
	}

	// A manual refresh of the paused account is written.
	manual, _, err := repo.RecordOllamaResult(ctx, OllamaCollectionResult{
		ID: account.ID, Status: "ok", ObservedAtMS: 6000, IsSuccess: true,
		SnapshotJSON:      `{"auth_index":"ollama:` + account.ID + `","provider":"ollama","windows":[]}`,
		CookieUpdatedAtMS: &paused.CookieUpdatedAtMS,
	})
	if err != nil {
		t.Fatal(err)
	}
	if manual.Status != "ok" {
		t.Fatalf("a paused manual refresh was dropped: %+v", manual)
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

// TestOllamaAccountAPIKeyRoundTripsWithoutMetadataExposure pins the key's storage
// contract: it is encrypted at rest, surfaced only as a boolean to metadata readers,
// and returned as ciphertext only to the collector.
func TestOllamaAccountAPIKeyRoundTripsWithoutMetadataExposure(t *testing.T) {
	repo, cipher := testRepository(t)
	ctx := context.Background()
	cookieCiphertext, cookieNonce, err := cipher.Encrypt([]byte("session-cookie"))
	if err != nil {
		t.Fatal(err)
	}
	const apiKey = "ollama-api-key-secret-value"
	keyCiphertext, keyNonce, err := cipher.Encrypt([]byte(apiKey))
	if err != nil {
		t.Fatal(err)
	}
	account, err := repo.CreateOllamaAccount(ctx, OllamaAccountCreate{
		Label:            "with-key",
		Enabled:          true,
		Ciphertext:       cookieCiphertext,
		Nonce:            cookieNonce,
		APIKeyCiphertext: keyCiphertext,
		APIKeyNonce:      keyNonce,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !account.HasCookie || !account.HasAPIKey {
		t.Fatalf("create result = %+v, want both presence flags", account)
	}

	var storedKey []byte
	if err := repo.SQL().QueryRowContext(ctx, `SELECT api_key_ciphertext FROM ollama_accounts WHERE id = ?`, account.ID).Scan(&storedKey); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(storedKey), apiKey) {
		t.Fatal("the API key was stored in plaintext")
	}

	listed, err := repo.ListOllamaAccounts(ctx)
	if err != nil || len(listed) != 1 {
		t.Fatalf("list = %+v %v", listed, err)
	}
	if !listed[0].HasAPIKey {
		t.Fatal("the metadata list must expose has_api_key")
	}
	read, found, err := repo.GetOllamaAccount(ctx, account.ID)
	if err != nil || !found || !read.HasAPIKey {
		t.Fatalf("metadata read = %+v found=%v err=%v", read, found, err)
	}

	secret, found, err := repo.GetOllamaAccountSecret(ctx, account.ID)
	if err != nil || !found {
		t.Fatalf("secret read = %v %v", found, err)
	}
	decrypted, err := cipher.Decrypt(secret.APIKeyCiphertext, secret.APIKeyNonce)
	if err != nil || string(decrypted) != apiKey {
		t.Fatalf("decrypt = %q %v", decrypted, err)
	}
	if secret.APIKeyUpdatedAtMS == 0 {
		t.Fatal("a stored key must carry a version")
	}
}

// TestOllamaAccountAPIKeyUpdateReplacesAndKeepsCookie pins that a key update replaces
// only the key, raising its version, while the cookie material stays untouched.
func TestOllamaAccountAPIKeyUpdateReplacesAndKeepsCookie(t *testing.T) {
	repo, cipher := testRepository(t)
	ctx := context.Background()
	cookieCiphertext, cookieNonce, _ := cipher.Encrypt([]byte("the-cookie"))
	firstKey, firstNonce, _ := cipher.Encrypt([]byte("first-key"))
	account, err := repo.CreateOllamaAccount(ctx, OllamaAccountCreate{
		Enabled: true, Ciphertext: cookieCiphertext, Nonce: cookieNonce,
		APIKeyCiphertext: firstKey, APIKeyNonce: firstNonce, NowMS: 1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	before, _, _ := repo.GetOllamaAccountSecret(ctx, account.ID)

	const secondKey = "second-key"
	secondCiphertext, secondNonce, _ := cipher.Encrypt([]byte(secondKey))
	updated, found, err := repo.UpdateOllamaAccount(ctx, account.ID, OllamaAccountUpdate{
		SetAPIKey: true, APIKeyCiphertext: secondCiphertext, APIKeyNonce: secondNonce, NowMS: 2000,
	})
	if err != nil || !found {
		t.Fatalf("update = %v %v", found, err)
	}
	if !updated.HasAPIKey || !updated.HasCookie {
		t.Fatalf("update result = %+v", updated)
	}
	after, _, _ := repo.GetOllamaAccountSecret(ctx, account.ID)
	if after.APIKeyUpdatedAtMS <= before.APIKeyUpdatedAtMS {
		t.Fatalf("key version did not advance: %d -> %d", before.APIKeyUpdatedAtMS, after.APIKeyUpdatedAtMS)
	}
	decrypted, err := cipher.Decrypt(after.APIKeyCiphertext, after.APIKeyNonce)
	if err != nil || string(decrypted) != secondKey {
		t.Fatalf("key replacement = %q %v", decrypted, err)
	}
	decryptedCookie, err := cipher.Decrypt(after.Ciphertext, after.Nonce)
	if err != nil || string(decryptedCookie) != "the-cookie" {
		t.Fatalf("an omitted cookie must be preserved, got %q %v", decryptedCookie, err)
	}
	if after.CookieUpdatedAtMS != before.CookieUpdatedAtMS {
		t.Fatalf("a key update moved the cookie version: %d -> %d", before.CookieUpdatedAtMS, after.CookieUpdatedAtMS)
	}
}

// TestDeleteOllamaAccountRemovesAPIKeyWithRow pins that the key columns disappear with
// the row rather than surviving as an orphan.
func TestDeleteOllamaAccountRemovesAPIKeyWithRow(t *testing.T) {
	repo, cipher := testRepository(t)
	ctx := context.Background()
	cookieCiphertext, cookieNonce, _ := cipher.Encrypt([]byte("cookie"))
	keyCiphertext, keyNonce, _ := cipher.Encrypt([]byte("key"))
	account, err := repo.CreateOllamaAccount(ctx, OllamaAccountCreate{
		Enabled: true, Ciphertext: cookieCiphertext, Nonce: cookieNonce,
		APIKeyCiphertext: keyCiphertext, APIKeyNonce: keyNonce,
	})
	if err != nil {
		t.Fatal(err)
	}
	removed, err := repo.DeleteOllamaAccount(ctx, account.ID)
	if err != nil || !removed {
		t.Fatalf("delete = %v %v", removed, err)
	}
	var keyCount int
	if err := repo.SQL().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM ollama_accounts WHERE id = ? AND api_key_ciphertext IS NOT NULL`, account.ID).Scan(&keyCount); err != nil {
		t.Fatal(err)
	}
	if keyCount != 0 {
		t.Fatalf("the deleted account still carries a key: %d", keyCount)
	}
}

// TestOllamaAPIKeyMigrationAddsNullableColumns pins the forward-only migration shape:
// the three key columns exist, are nullable and carry no default, so existing rows
// are untouched and "never had a key" stays distinguishable.
func TestOllamaAPIKeyMigrationAddsNullableColumns(t *testing.T) {
	repo, _ := testRepository(t)
	rows, err := repo.SQL().QueryContext(context.Background(), `SELECT name, "notnull", dflt_value FROM pragma_table_info('ollama_accounts')`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns := map[string]struct {
		notNull    bool
		hasDefault bool
	}{}
	for rows.Next() {
		var name string
		var notNull int
		var dflt any
		if err := rows.Scan(&name, &notNull, &dflt); err != nil {
			t.Fatal(err)
		}
		columns[name] = struct {
			notNull    bool
			hasDefault bool
		}{notNull: notNull != 0, hasDefault: dflt != nil}
	}
	for _, name := range []string{"api_key_ciphertext", "api_key_nonce", "api_key_updated_at_ms"} {
		column, ok := columns[name]
		if !ok {
			t.Fatalf("column %s was not added", name)
		}
		if column.notNull {
			t.Fatalf("column %s must be nullable", name)
		}
		if column.hasDefault {
			t.Fatalf("column %s must have no default", name)
		}
	}
}
