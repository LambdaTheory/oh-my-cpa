package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/oh-my-cpa/oh-my-cpa/internal/config"
	"github.com/oh-my-cpa/oh-my-cpa/internal/crypto"
	"github.com/oh-my-cpa/oh-my-cpa/internal/feishu"
	"github.com/oh-my-cpa/oh-my-cpa/internal/repository"
)

type apiPredictionSender struct {
	mu    sync.Mutex
	sends int
	err   error
}

func (s *apiPredictionSender) SendCard(context.Context, string, map[string]any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sends++
	return s.err
}
func (s *apiPredictionSender) ReplyText(context.Context, string, string) error { return nil }
func (s *apiPredictionSender) ReplyCard(context.Context, string, map[string]any) error {
	return nil
}
func (s *apiPredictionSender) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sends
}

// newFeishuAPITest builds a handler, repository and Feishu API over an in-memory
// database and serves the Feishu routes on a bare chi router, so routing and the
// DTO shape are exercised without the shared routing table or console auth.
func newFeishuAPITest(t *testing.T) (*FeishuAPI, *repository.Repository, *crypto.Cipher, *httptest.Server) {
	t.Helper()
	cipher, err := crypto.New("01234567890123456789012345678901")
	if err != nil {
		t.Fatal(err)
	}
	db, err := repository.Open(context.Background(), dashboardMemoryDSN("feishu-api"), repository.WithCipher(cipher))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	repo := repository.New(db)
	handler := NewHandler(config.Config{BasePath: "/omc", Version: "test"}, repo, cipher, nil, nil)
	api, err := handler.BuildFeishuAPI(context.Background(), nil, feishu.Overlay{})
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	RegisterFeishuRoutes(router, api)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	return api, repo, cipher, server
}

func TestFeishuSettingsDTOHidesSecret(t *testing.T) {
	_, _, _, server := newFeishuAPITest(t)
	const secretValue = "super-secret-app-secret"

	// Initially no secret.
	response, err := http.Get(server.URL + "/feishu/settings")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := readAll(response)
	if !strings.Contains(string(body), `"has_secret":false`) {
		t.Fatalf("expected has_secret false initially: %s", body)
	}

	// Save a secret through the write route.
	payload := map[string]any{
		"enabled":       true,
		"app_id":        "cli_test",
		"app_secret":    secretValue,
		"chat_id":       "oc_test",
		"summary_times": []string{"10:00", "17:00"},
		"quiet_start":   "23:00",
		"quiet_end":     "08:00",
	}
	encoded, _ := json.Marshal(payload)
	putResponse, err := http.NewRequest(http.MethodPut, server.URL+"/feishu/settings", bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	putResponse.Header.Set("Content-Type", "application/json")
	saved, err := http.DefaultClient.Do(putResponse)
	if err != nil {
		t.Fatal(err)
	}
	savedBody, _ := readAll(saved)
	if saved.StatusCode != http.StatusOK {
		t.Fatalf("PUT settings status = %d body=%s", saved.StatusCode, savedBody)
	}
	if strings.Contains(string(savedBody), secretValue) {
		t.Fatalf("settings response leaked the secret: %s", savedBody)
	}
	if !strings.Contains(string(savedBody), `"has_secret":true`) {
		t.Fatalf("expected has_secret true after save: %s", savedBody)
	}

	// A read never returns the secret either.
	readResponse, err := http.Get(server.URL + "/feishu/settings")
	if err != nil {
		t.Fatal(err)
	}
	readBody, _ := readAll(readResponse)
	if strings.Contains(string(readBody), secretValue) {
		t.Fatalf("settings read leaked the secret: %s", readBody)
	}
	if !strings.Contains(string(readBody), `"has_secret":true`) {
		t.Fatalf("expected has_secret true after save: %s", readBody)
	}

	// Status is a distinct route and carries no secret.
	statusResponse, err := http.Get(server.URL + "/feishu/status")
	if err != nil {
		t.Fatal(err)
	}
	statusBody, _ := readAll(statusResponse)
	if strings.Contains(string(statusBody), secretValue) {
		t.Fatalf("status leaked the secret: %s", statusBody)
	}
}

func TestFeishuSecretIsEncryptedAtRest(t *testing.T) {
	api, repo, cipher, _ := newFeishuAPITest(t)
	const secretValue = "another-secret-value"
	if err := api.saveSecret(context.Background(), secretValue); err != nil {
		t.Fatal(err)
	}
	ciphertext, nonce, has, err := repo.LoadFeishuSecret(context.Background())
	if err != nil || !has {
		t.Fatalf("secret not stored: has=%v err=%v", has, err)
	}
	if bytes.Contains(ciphertext, []byte(secretValue)) {
		t.Fatal("the stored ciphertext must not contain the plaintext secret")
	}
	plaintext, err := cipher.Decrypt(ciphertext, nonce)
	if err != nil {
		t.Fatal(err)
	}
	if string(plaintext) != secretValue {
		t.Fatalf("decrypted secret = %q", plaintext)
	}
}

func TestFeishuSecretWriteWithoutCipherHidesSecret(t *testing.T) {
	db, err := repository.Open(context.Background(), dashboardMemoryDSN("feishu-nocipher"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	repo := repository.New(db)
	handler := NewHandler(config.Config{BasePath: "/omc", Version: "test"}, repo, nil, nil, nil)
	api, err := handler.BuildFeishuAPI(context.Background(), nil, feishu.Overlay{})
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	RegisterFeishuRoutes(router, api)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	const secretValue = "leaky-secret-value"
	encoded, _ := json.Marshal(map[string]any{"enabled": true, "app_id": "cli_x", "chat_id": "oc_x", "app_secret": secretValue})
	request, _ := http.NewRequest(http.MethodPut, server.URL+"/feishu/settings", bytes.NewReader(encoded))
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := readAll(response)
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 without a cipher, got %d body=%s", response.StatusCode, body)
	}
	if strings.Contains(string(body), secretValue) {
		t.Fatalf("the error response leaked the secret: %s", body)
	}
	if _, _, has, _ := repo.LoadFeishuSecret(context.Background()); has {
		t.Fatal("no secret should be stored when the cipher is unavailable")
	}
}

func TestFeishuTestRouteUsesInjectedSender(t *testing.T) {
	// The test route must not reach Feishu in a test: the service is inactive here
	// (no configuration), so it fails fast rather than connecting.
	_, _, _, server := newFeishuAPITest(t)
	request, _ := http.NewRequest(http.MethodPost, server.URL+"/feishu/test", nil)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := readAll(response)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("test route status = %d body=%s", response.StatusCode, body)
	}
	if !strings.Contains(string(body), `"sent":false`) {
		t.Fatalf("expected sent=false for an inactive module: %s", body)
	}
}

func TestFeishuPredictionTestRouteSendsOneCardAndReportsNoFindings(t *testing.T) {
	now := time.Date(2026, 10, 20, 8, 0, 0, 0, time.UTC)
	sender := &apiPredictionSender{}
	store := feishu.NewMemoryStore()
	secret := "fake-secret"
	service := feishu.NewService(feishu.Options{
		Web:     feishu.Config{Enabled: true, AppID: "fake-app", ChatID: "fake-chat", Timezone: "UTC"},
		Overlay: feishu.Overlay{AppSecret: &secret}, Store: store, Now: func() time.Time { return now },
		SenderFactory: func(feishu.Resolved) feishu.Sender { return sender },
		SourceFactory: func(feishu.Resolved) feishu.EventSource { return nil },
	})
	used, projected := 60.0, 120.0
	digest := feishu.PredictionDigest{Items: []feishu.PredictionDigestItem{{
		CredentialLabel: "operator", Provider: "codex", WindowKind: "weekly",
		UsedPercent: &used, ProjectedPercentAtReset: &projected,
		ObservedAtMS: now.UnixMilli(), Verdict: "will_exhaust",
	}}}
	api := &FeishuAPI{service: service}
	api.prediction = feishu.NewPredictionJob(service, func(context.Context) (feishu.PredictionDigest, error) { return digest, nil })
	router := chi.NewRouter()
	RegisterFeishuRoutes(router, api)

	request := httptest.NewRequest(http.MethodPost, "/feishu/prediction-test", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"sent":true`) || sender.count() != 1 {
		t.Fatalf("finding response=%d %s sends=%d", recorder.Code, recorder.Body.String(), sender.count())
	}
	state, err := store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !state.LastSummaryAt.IsZero() {
		t.Fatalf("manual send changed last summary: %s", state.LastSummaryAt)
	}

	digest = feishu.PredictionDigest{Items: []feishu.PredictionDigestItem{{Verdict: "on_track"}}}
	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/feishu/prediction-test", nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"sent":false`) || !strings.Contains(recorder.Body.String(), `"reason":"no_findings"`) {
		t.Fatalf("no-finding response=%d %s", recorder.Code, recorder.Body.String())
	}
	if sender.count() != 1 {
		t.Fatalf("no-finding request sent another card: %d", sender.count())
	}

	now = now.Add(time.Minute)
	sender.err = errors.New("simulated sender failure")
	digest = feishu.PredictionDigest{Items: []feishu.PredictionDigestItem{{CredentialLabel: "operator", Verdict: "will_exhaust"}}}
	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/feishu/prediction-test", nil))
	var failed struct {
		Sent  bool   `json:"sent"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &failed); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusOK || failed.Sent || failed.Error == "" {
		t.Fatalf("failed delivery response=%d %s", recorder.Code, recorder.Body.String())
	}
	state, err = store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Outbox) != 2 || state.Outbox[1].State != feishu.NotifyPending || !state.Outbox[1].NextAttemptAt.After(now) {
		t.Fatalf("failed route delivery must retain retry: %+v", state.Outbox)
	}
}

func readAll(response *http.Response) ([]byte, error) {
	defer response.Body.Close()
	buffer := new(bytes.Buffer)
	if _, err := buffer.ReadFrom(response.Body); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}
