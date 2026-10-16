package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/oh-my-cpa/oh-my-cpa/internal/config"
	"github.com/oh-my-cpa/oh-my-cpa/internal/crypto"
	"github.com/oh-my-cpa/oh-my-cpa/internal/repository"
)

const ollamaTestCookie = "super-secret-ollama-cookie-value"

func startOllamaTestServer(t *testing.T) (*httptest.Server, *repository.Repository, *crypto.Cipher) {
	t.Helper()
	cipher, err := crypto.New("01234567890123456789012345678901")
	if err != nil {
		t.Fatal(err)
	}
	db, err := repository.Open(context.Background(), dashboardMemoryDSN("ollama"), repository.WithCipher(cipher))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	repo := repository.New(db)
	handler := NewHandler(config.Config{BasePath: "/omc", Version: "test"}, repo, cipher, nil, nil)
	router := chi.NewRouter()
	// RegisterOllamaRoutes is the standalone registration the integration owner calls
	// inside the authenticated v1 group; here it is mounted raw so the handlers can be
	// exercised without the shared routing table.
	RegisterOllamaRoutes(router, handler)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	return server, repo, cipher
}

func ollamaRequest(t *testing.T, method, url, body string) (int, []byte) {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = bytes.NewBufferString(body)
	}
	request, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, payload
}

// TestOllamaAccountResponsesNeverLeakCookie pins that create, list, detail and update
// responses carry has_cookie but never the cookie text, and that an update without a
// cookie preserves the stored one.
func TestOllamaAccountResponsesNeverLeakCookie(t *testing.T) {
	server, repo, cipher := startOllamaTestServer(t)

	status, payload := ollamaRequest(t, http.MethodPost, server.URL+"/ollama/accounts",
		`{"label":"work","cookie":"`+ollamaTestCookie+`","enabled":true}`)
	if status != http.StatusCreated {
		t.Fatalf("create status = %d: %s", status, payload)
	}
	assertNoCookie(t, payload)
	var created ollamaAccountDTO
	if err := json.Unmarshal(payload, &created); err != nil {
		t.Fatal(err)
	}
	if !created.HasCookie || created.ID == "" {
		t.Fatalf("create dto = %+v", created)
	}

	status, payload = ollamaRequest(t, http.MethodGet, server.URL+"/ollama/accounts", "")
	if status != http.StatusOK {
		t.Fatalf("list status = %d", status)
	}
	assertNoCookie(t, payload)
	if !strings.Contains(string(payload), `"has_cookie":true`) {
		t.Fatalf("list must report has_cookie: %s", payload)
	}

	status, payload = ollamaRequest(t, http.MethodGet, server.URL+"/ollama/accounts/"+created.ID, "")
	if status != http.StatusOK {
		t.Fatalf("detail status = %d", status)
	}
	assertNoCookie(t, payload)

	// An update with no cookie keeps the stored one.
	status, payload = ollamaRequest(t, http.MethodPut, server.URL+"/ollama/accounts/"+created.ID, `{"label":"renamed"}`)
	if status != http.StatusOK {
		t.Fatalf("update status = %d: %s", status, payload)
	}
	assertNoCookie(t, payload)
	if !strings.Contains(string(payload), `"has_cookie":true`) {
		t.Fatalf("update must keep has_cookie: %s", payload)
	}
	secrets, err := repo.ListEnabledOllamaAccountSecrets(context.Background())
	if err != nil || len(secrets) != 1 {
		t.Fatalf("secrets = %+v %v", secrets, err)
	}
	decrypted, err := cipher.Decrypt(secrets[0].Ciphertext, secrets[0].Nonce)
	if err != nil || string(decrypted) != ollamaTestCookie {
		t.Fatalf("an omitted cookie must be preserved: %q %v", decrypted, err)
	}

	// A present but blank cookie is refused rather than clearing the credential.
	if status, _ := ollamaRequest(t, http.MethodPut, server.URL+"/ollama/accounts/"+created.ID, `{"cookie":""}`); status != http.StatusBadRequest {
		t.Fatalf("blank cookie update status = %d, want 400", status)
	}

	if status, _ := ollamaRequest(t, http.MethodDelete, server.URL+"/ollama/accounts/"+created.ID, ""); status != http.StatusNoContent {
		t.Fatalf("delete status = %d", status)
	}
}

func TestOllamaQuotaReadIsReadOnly(t *testing.T) {
	server, repo, _ := startOllamaTestServer(t)
	status, payload := ollamaRequest(t, http.MethodPost, server.URL+"/ollama/accounts",
		`{"label":"work","cookie":"`+ollamaTestCookie+`"}`)
	if status != http.StatusCreated {
		t.Fatalf("create status = %d: %s", status, payload)
	}
	var created ollamaAccountDTO
	if err := json.Unmarshal(payload, &created); err != nil {
		t.Fatal(err)
	}
	snapshot := `{"auth_index":"ollama:` + created.ID + `","provider":"ollama","windows":[]}`
	if _, _, err := repo.RecordOllamaResult(context.Background(), repository.OllamaCollectionResult{
		ID: created.ID, Status: "ok", ObservedAtMS: 5000, IsSuccess: true, SnapshotJSON: snapshot,
	}); err != nil {
		t.Fatal(err)
	}
	status, payload = ollamaRequest(t, http.MethodGet, server.URL+"/ollama/quota", "")
	if status != http.StatusOK {
		t.Fatalf("quota status = %d", status)
	}
	assertNoCookie(t, payload)
	if !strings.Contains(string(payload), `"observed_at_ms":5000`) || !strings.Contains(string(payload), `ollama:`+created.ID) {
		t.Fatalf("quota body = %s", payload)
	}
}

func assertNoCookie(t *testing.T, payload []byte) {
	t.Helper()
	if strings.Contains(string(payload), ollamaTestCookie) {
		t.Fatalf("the cookie leaked into a response: %s", payload)
	}
}
