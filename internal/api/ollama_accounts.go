package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/oh-my-cpa/oh-my-cpa/internal/ollama"
	"github.com/oh-my-cpa/oh-my-cpa/internal/quota"
	"github.com/oh-my-cpa/oh-my-cpa/internal/repository"
)

// Ollama account management API (lane B of the first batch).
//
// The routes are session-authenticated and same-origin guarded exactly like every
// other management write; the read-only MCP key reaches none of them. The DTOs are
// a deliberate allowlist: they carry a has_cookie flag and no secret, and the cookie
// is accepted on write only. The cookie never appears in DTOs, logs, errors or audit
// details.

const (
	ollamaRequestLimit = 32 << 10
	ollamaLabelLimit   = 120
	ollamaCookieLimit  = 8 << 10
)

// errOllamaCipherUnavailable is returned when a cookie write is attempted without a
// configured master-key cipher. It carries no cookie material.
var errOllamaCipherUnavailable = errors.New("ollama: master-key cipher is not configured")

type ollamaAccountDTO struct {
	ID               string `json:"id"`
	Label            string `json:"label,omitempty"`
	Enabled          bool   `json:"enabled"`
	HasCookie        bool   `json:"has_cookie"`
	Status           string `json:"status,omitempty"`
	LastSuccessAtMS  *int64 `json:"last_success_at_ms,omitempty"`
	LastObservedAtMS *int64 `json:"last_observed_at_ms,omitempty"`
	NewestError      string `json:"newest_error,omitempty"`
	NewestErrorAtMS  *int64 `json:"newest_error_at_ms,omitempty"`
	NextAttemptAtMS  *int64 `json:"next_attempt_at_ms,omitempty"`
	AttemptCount     int    `json:"attempt_count"`
	CreatedAtMS      int64  `json:"created_at_ms"`
	UpdatedAtMS      int64  `json:"updated_at_ms"`
}

type ollamaAccountWriteDTO struct {
	Label   *string `json:"label"`
	Cookie  *string `json:"cookie"`
	Enabled *bool   `json:"enabled"`
}

type ollamaAccountListResponse struct {
	Accounts []ollamaAccountDTO `json:"accounts"`
}

type ollamaQuotaDTO struct {
	AccountID    string          `json:"account_id"`
	Label        string          `json:"label,omitempty"`
	Enabled      bool            `json:"enabled"`
	Status       string          `json:"status,omitempty"`
	ObservedAtMS *int64          `json:"observed_at_ms,omitempty"`
	Quota        json.RawMessage `json:"quota,omitempty"`
}

type ollamaQuotaResponse struct {
	Accounts []ollamaQuotaDTO `json:"accounts"`
}

// RegisterOllamaRoutes installs the Ollama account routes on the authenticated v1
// router. It is called inside the /api/v1 group in handler.go.
func RegisterOllamaRoutes(router chi.Router, h *Handler) {
	router.Get("/ollama/accounts", h.listOllamaAccounts)
	router.Post("/ollama/accounts", h.createOllamaAccount)
	router.Get("/ollama/accounts/{id}", h.getOllamaAccount)
	router.Put("/ollama/accounts/{id}", h.updateOllamaAccount)
	router.Delete("/ollama/accounts/{id}", h.deleteOllamaAccount)
	router.Get("/ollama/quota", h.getOllamaQuota)
}

// BuildOllamaService builds the collector the background loop runs. It adapts the
// repository and the cipher to the collector's narrow view, keeping the ollama
// package free of the HTTP and repository layers. The application starts it with
// Run in the same place the account-automation manager runs.
func (h *Handler) BuildOllamaService() *ollama.Collector {
	if h.repo == nil {
		return nil
	}
	timeout := h.cfg.Ollama.Timeout
	if timeout <= 0 {
		timeout = ollama.DefaultTimeout
	}
	client := ollama.NewClient(timeout)
	return &ollama.Collector{
		Accounts: h.ollamaAccountsForCollection,
		Save:     h.saveOllamaResult,
		Decrypt: func(ciphertext, nonce []byte) ([]byte, error) {
			if h.cipher == nil {
				return nil, errOllamaCipherUnavailable
			}
			return h.cipher.Decrypt(ciphertext, nonce)
		},
		Fetch: func(ctx context.Context, account ollama.Account, cookie string) (ollama.Snapshot, error) {
			return client.Fetch(ctx, account.ID, cookie)
		},
		Observations: h.recordOllamaObservations,
		Audit: func(ctx context.Context, action, result, targetID string, details map[string]any) {
			h.recordAuditWithoutRequest(action, "ollama_account", targetID, result, details)
		},
		Logger:      h.logger,
		Now:         h.now,
		Interval:    ollama.DefaultInterval,
		BackoffBase: ollama.DefaultBackoffBase,
		BackoffMax:  ollama.DefaultBackoffMax,
	}
}

func (h *Handler) ollamaAccountsForCollection(ctx context.Context) ([]ollama.StoredAccount, error) {
	if h.repo == nil {
		return nil, errors.New("repository is not initialized")
	}
	secrets, err := h.repo.ListEnabledOllamaAccountSecrets(ctx)
	if err != nil {
		return nil, err
	}
	accounts := make([]ollama.StoredAccount, 0, len(secrets))
	for _, secret := range secrets {
		accounts = append(accounts, ollama.StoredAccount{
			Account: ollama.Account{
				ID:              secret.ID,
				Label:           secret.Label,
				Enabled:         secret.Enabled,
				NextAttemptAtMS: secret.NextAttemptAtMS,
				AttemptCount:    secret.AttemptCount,
			},
			Ciphertext: secret.Ciphertext,
			Nonce:      secret.Nonce,
		})
	}
	return accounts, nil
}

func (h *Handler) saveOllamaResult(ctx context.Context, result ollama.CollectionResult) error {
	if h.repo == nil {
		return errors.New("repository is not initialized")
	}
	_, _, err := h.repo.RecordOllamaResult(ctx, repository.OllamaCollectionResult{
		ID:              result.ID,
		Status:          result.Status,
		ObservedAtMS:    result.ObservedAtMS,
		IsSuccess:       result.IsSuccess,
		SnapshotJSON:    result.SnapshotJSON,
		Error:           result.Error,
		ErrorAtMS:       result.ErrorAtMS,
		NextAttemptAtMS: result.NextAttemptAtMS,
		AttemptCount:    result.AttemptCount,
	})
	return err
}

func (h *Handler) recordOllamaObservations(ctx context.Context, value *quota.NormalizedQuota) error {
	if h.repo == nil {
		return errors.New("repository is not initialized")
	}
	_, err := h.repo.RecordActiveQuotaObservations(ctx, value)
	return err
}

func (h *Handler) listOllamaAccounts(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	if h.repo == nil {
		writeError(writer, http.StatusServiceUnavailable, "ollama account storage is unavailable")
		return
	}
	accounts, err := h.repo.ListOllamaAccounts(request.Context())
	if err != nil {
		writeInternalError(writer, err)
		return
	}
	dtos := make([]ollamaAccountDTO, 0, len(accounts))
	for _, account := range accounts {
		dtos = append(dtos, ollamaAccountToDTO(account))
	}
	writeJSON(writer, http.StatusOK, ollamaAccountListResponse{Accounts: dtos})
}

func (h *Handler) getOllamaAccount(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	id, ok := ollamaAccountID(writer, request)
	if !ok {
		return
	}
	if h.repo == nil {
		writeError(writer, http.StatusServiceUnavailable, "ollama account storage is unavailable")
		return
	}
	account, found, err := h.repo.GetOllamaAccount(request.Context(), id)
	if err != nil {
		writeInternalError(writer, err)
		return
	}
	if !found {
		writeError(writer, http.StatusNotFound, "ollama account not found")
		return
	}
	writeJSON(writer, http.StatusOK, ollamaAccountToDTO(account))
}

func (h *Handler) createOllamaAccount(writer http.ResponseWriter, request *http.Request) {
	if h.repo == nil {
		writeError(writer, http.StatusServiceUnavailable, "ollama account storage is unavailable")
		return
	}
	var payload ollamaAccountWriteDTO
	if err := decodeManagementJSON(writer, request, ollamaRequestLimit, &payload); err != nil {
		return
	}
	if payload.Cookie == nil || strings.TrimSpace(*payload.Cookie) == "" {
		writeError(writer, http.StatusBadRequest, "cookie is required")
		return
	}
	if len(*payload.Cookie) > ollamaCookieLimit {
		writeError(writer, http.StatusBadRequest, "cookie is too long")
		return
	}
	label := ""
	if payload.Label != nil {
		label = strings.TrimSpace(*payload.Label)
	}
	if len(label) > ollamaLabelLimit {
		writeError(writer, http.StatusBadRequest, "label is too long")
		return
	}
	ciphertext, nonce, err := h.encryptOllamaCookie(*payload.Cookie)
	if err != nil {
		h.writeOllamaCipherError(writer, err)
		return
	}
	enabled := true
	if payload.Enabled != nil {
		enabled = *payload.Enabled
	}
	account, err := h.repo.CreateOllamaAccount(request.Context(), repository.OllamaAccountCreate{
		Label:      label,
		Enabled:    enabled,
		Ciphertext: ciphertext,
		Nonce:      nonce,
		NowMS:      h.now().UnixMilli(),
	})
	if err != nil {
		writeInternalError(writer, err)
		return
	}
	h.recordAuditWithoutRequest("ollama.account.create", "ollama_account", account.ID, "success", map[string]any{"label": label})
	writeJSON(writer, http.StatusCreated, ollamaAccountToDTO(account))
}

func (h *Handler) updateOllamaAccount(writer http.ResponseWriter, request *http.Request) {
	id, ok := ollamaAccountID(writer, request)
	if !ok {
		return
	}
	if h.repo == nil {
		writeError(writer, http.StatusServiceUnavailable, "ollama account storage is unavailable")
		return
	}
	var payload ollamaAccountWriteDTO
	if err := decodeManagementJSON(writer, request, ollamaRequestLimit, &payload); err != nil {
		return
	}
	update := repository.OllamaAccountUpdate{NowMS: h.now().UnixMilli()}
	if payload.Label != nil {
		label := strings.TrimSpace(*payload.Label)
		if len(label) > ollamaLabelLimit {
			writeError(writer, http.StatusBadRequest, "label is too long")
			return
		}
		update.Label = &label
	}
	if payload.Enabled != nil {
		update.Enabled = payload.Enabled
	}
	// An omitted cookie keeps the stored value; a present but blank one is refused
	// rather than silently clearing the credential.
	if payload.Cookie != nil {
		if strings.TrimSpace(*payload.Cookie) == "" {
			writeError(writer, http.StatusBadRequest, "cookie must not be empty")
			return
		}
		if len(*payload.Cookie) > ollamaCookieLimit {
			writeError(writer, http.StatusBadRequest, "cookie is too long")
			return
		}
		ciphertext, nonce, err := h.encryptOllamaCookie(*payload.Cookie)
		if err != nil {
			h.writeOllamaCipherError(writer, err)
			return
		}
		update.SetCookie = true
		update.Ciphertext = ciphertext
		update.Nonce = nonce
	}
	account, found, err := h.repo.UpdateOllamaAccount(request.Context(), id, update)
	if err != nil {
		writeInternalError(writer, err)
		return
	}
	if !found {
		writeError(writer, http.StatusNotFound, "ollama account not found")
		return
	}
	h.recordAuditWithoutRequest("ollama.account.update", "ollama_account", id, "success", map[string]any{"cookie_changed": update.SetCookie})
	writeJSON(writer, http.StatusOK, ollamaAccountToDTO(account))
}

func (h *Handler) deleteOllamaAccount(writer http.ResponseWriter, request *http.Request) {
	id, ok := ollamaAccountID(writer, request)
	if !ok {
		return
	}
	if h.repo == nil {
		writeError(writer, http.StatusServiceUnavailable, "ollama account storage is unavailable")
		return
	}
	removed, err := h.repo.DeleteOllamaAccount(request.Context(), id)
	if err != nil {
		writeInternalError(writer, err)
		return
	}
	if !removed {
		writeError(writer, http.StatusNotFound, "ollama account not found")
		return
	}
	h.recordAuditWithoutRequest("ollama.account.delete", "ollama_account", id, "success", nil)
	writer.WriteHeader(http.StatusNoContent)
}

// getOllamaQuota serves the newest stored snapshot, read-only. It never triggers a
// scrape: the operator asked for what is known, not for a refresh.
func (h *Handler) getOllamaQuota(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	if h.repo == nil {
		writeError(writer, http.StatusServiceUnavailable, "ollama account storage is unavailable")
		return
	}
	accounts, err := h.repo.ListOllamaAccounts(request.Context())
	if err != nil {
		writeInternalError(writer, err)
		return
	}
	dtos := make([]ollamaQuotaDTO, 0, len(accounts))
	for _, account := range accounts {
		dto := ollamaQuotaDTO{
			AccountID:    account.ID,
			Label:        account.Label,
			Enabled:      account.Enabled,
			Status:       account.Status,
			ObservedAtMS: account.QuotaObservedAtMS,
		}
		if strings.TrimSpace(account.QuotaSnapshotJSON) != "" {
			dto.Quota = json.RawMessage(account.QuotaSnapshotJSON)
		}
		dtos = append(dtos, dto)
	}
	writeJSON(writer, http.StatusOK, ollamaQuotaResponse{Accounts: dtos})
}

func (h *Handler) encryptOllamaCookie(cookie string) ([]byte, []byte, error) {
	if h.cipher == nil {
		return nil, nil, errOllamaCipherUnavailable
	}
	return h.cipher.Encrypt([]byte(cookie))
}

// writeOllamaCipherError refuses a cookie write when no master key is configured. It
// logs the reason but never the cookie.
func (h *Handler) writeOllamaCipherError(writer http.ResponseWriter, err error) {
	h.logger.Warn("ollama cookie could not be encrypted", "error", err)
	writeError(writer, http.StatusServiceUnavailable, "master key is not configured; the cookie cannot be stored")
}

func ollamaAccountID(writer http.ResponseWriter, request *http.Request) (string, bool) {
	id := strings.TrimSpace(chi.URLParam(request, "id"))
	if id == "" || len(id) > 128 {
		writeError(writer, http.StatusBadRequest, "invalid ollama account id")
		return "", false
	}
	return id, true
}

func ollamaAccountToDTO(account repository.OllamaAccount) ollamaAccountDTO {
	return ollamaAccountDTO{
		ID:               account.ID,
		Label:            account.Label,
		Enabled:          account.Enabled,
		HasCookie:        account.HasCookie,
		Status:           account.Status,
		LastSuccessAtMS:  account.LastSuccessAtMS,
		LastObservedAtMS: account.LastObservedAtMS,
		NewestError:      account.NewestError,
		NewestErrorAtMS:  account.NewestErrorAtMS,
		NextAttemptAtMS:  account.NextAttemptAtMS,
		AttemptCount:     account.AttemptCount,
		CreatedAtMS:      account.CreatedAtMS,
		UpdatedAtMS:      account.UpdatedAtMS,
	}
}
