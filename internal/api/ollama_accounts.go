package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

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
	// ollamaRefreshTimeout bounds how long the synchronous manual refresh waits. The
	// underlying scrape already carries its own client timeout; this only stops a
	// caller from being held open indefinitely behind an in-flight periodic pass.
	ollamaRefreshTimeout = 20 * time.Second
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
	// Refreshing is true while a scrape for this account is in flight, so the
	// console can show progress instead of offering a second refresh.
	Refreshing bool `json:"refreshing"`
	// RetryAtMS is the earliest instant a refresh is allowed again, driven by a
	// persisted backoff or the manual minimum gap. Absent when nothing is pending.
	RetryAtMS *int64 `json:"retry_at_ms,omitempty"`
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
	AccountID    string                 `json:"account_id"`
	Label        string                 `json:"label,omitempty"`
	Enabled      bool                   `json:"enabled"`
	Status       string                 `json:"status,omitempty"`
	ObservedAtMS *int64                 `json:"observed_at_ms,omitempty"`
	Quota        *quota.NormalizedQuota `json:"quota,omitempty"`
}

type ollamaQuotaResponse struct {
	Accounts []ollamaQuotaDTO `json:"accounts"`
}

// ollamaRefreshResponse is the manual refresh contract. The account reflects the
// stored metadata after the attempt (so a failure is readable through its status
// and newest_error), quota is the newest stored safe snapshot - never a raw body -
// and refreshed says whether a scrape actually ran for this request. retry_at_ms is
// set when the request was suppressed by a backoff or the manual gap.
type ollamaRefreshResponse struct {
	Account   ollamaAccountDTO       `json:"account"`
	Quota     *quota.NormalizedQuota `json:"quota,omitempty"`
	Refreshed bool                   `json:"refreshed"`
	RetryAtMS *int64                 `json:"retry_at_ms,omitempty"`
}

// RegisterOllamaRoutes installs the Ollama account routes on the authenticated v1
// router. It is called inside the /api/v1 group in handler.go.
func RegisterOllamaRoutes(router chi.Router, h *Handler) {
	router.Get("/ollama/accounts", h.listOllamaAccounts)
	router.Post("/ollama/accounts", h.createOllamaAccount)
	router.Get("/ollama/accounts/{id}", h.getOllamaAccount)
	router.Put("/ollama/accounts/{id}", h.updateOllamaAccount)
	router.Delete("/ollama/accounts/{id}", h.deleteOllamaAccount)
	router.Post("/ollama/accounts/{id}/refresh", h.refreshOllamaAccount)
	router.Get("/ollama/accounts/{id}/diagnose", h.diagnoseOllamaAccount)
	router.Get("/ollama/quota", h.getOllamaQuota)
}

// BuildOllamaService builds the collector the background loop runs. It adapts the
// repository and the cipher to the collector's narrow view, keeping the ollama
// package free of the HTTP and repository layers. The application starts it with
// Run in the same place the account-automation manager runs, and hands the same
// instance back through SetOllamaService so the manual refresh and the triggers
// share its lock, concurrency cap and backoff.
func (h *Handler) BuildOllamaService() *ollama.Collector {
	if h.repo == nil {
		return nil
	}
	timeout := h.cfg.Ollama.Timeout
	if timeout <= 0 {
		timeout = ollama.DefaultTimeout
	}
	interval := h.cfg.Ollama.Interval
	if interval <= 0 {
		interval = ollama.DefaultInterval
	}
	backoffBase := h.cfg.Ollama.BackoffBase
	if backoffBase <= 0 {
		backoffBase = ollama.DefaultBackoffBase
	}
	backoffMax := h.cfg.Ollama.BackoffMax
	if backoffMax < backoffBase {
		backoffMax = backoffBase
	}
	client := ollama.NewClient(timeout)
	return &ollama.Collector{
		Accounts:    h.ollamaAccountsForCollection,
		AccountByID: h.ollamaAccountSecretByID,
		Save:        h.saveOllamaResult,
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
		Logger:         h.logger,
		Now:            h.now,
		Interval:       interval,
		BackoffBase:    backoffBase,
		BackoffMax:     backoffMax,
		Concurrency:    ollama.DefaultConcurrency,
		RefreshTimeout: ollamaRefreshTimeout,
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
			Ciphertext:        secret.Ciphertext,
			Nonce:             secret.Nonce,
			CookieUpdatedAtMS: secret.CookieUpdatedAtMS,
		})
	}
	return accounts, nil
}

// ollamaAccountSecretByID loads the newest stored secret for one account, enabled or
// not. It is the reload the refresh paths use so a queued scrape reads the cookie as
// it is now, not as it was when the account was listed.
func (h *Handler) ollamaAccountSecretByID(ctx context.Context, id string) (ollama.StoredAccount, bool, error) {
	if h.repo == nil {
		return ollama.StoredAccount{}, false, errors.New("repository is not initialized")
	}
	secret, found, err := h.repo.GetOllamaAccountSecret(ctx, id)
	if err != nil || !found {
		return ollama.StoredAccount{}, found, err
	}
	return ollama.StoredAccount{
		Account: ollama.Account{
			ID:              secret.ID,
			Label:           secret.Label,
			Enabled:         secret.Enabled,
			NextAttemptAtMS: secret.NextAttemptAtMS,
			AttemptCount:    secret.AttemptCount,
		},
		Ciphertext:        secret.Ciphertext,
		Nonce:             secret.Nonce,
		CookieUpdatedAtMS: secret.CookieUpdatedAtMS,
	}, true, nil
}

func (h *Handler) saveOllamaResult(ctx context.Context, result ollama.CollectionResult) error {
	if h.repo == nil {
		return errors.New("repository is not initialized")
	}
	_, _, err := h.repo.RecordOllamaResult(ctx, repository.OllamaCollectionResult{
		ID:                result.ID,
		Status:            result.Status,
		ObservedAtMS:      result.ObservedAtMS,
		IsSuccess:         result.IsSuccess,
		SnapshotJSON:      result.SnapshotJSON,
		Error:             result.Error,
		ErrorAtMS:         result.ErrorAtMS,
		NextAttemptAtMS:   result.NextAttemptAtMS,
		AttemptCount:      result.AttemptCount,
		CookieUpdatedAtMS: result.CookieUpdatedAtMS,
		Auto:              result.Auto,
	})
	return err
}

// triggerOllamaRefresh queues an opportunistic scrape when the shared service
// exists. It is nil-safe: with the collector off - or in a demo - nothing is
// queued, and no fallback collector is started.
func (h *Handler) triggerOllamaRefresh(id string) {
	if h.ollama == nil {
		return
	}
	h.ollama.Trigger(id)
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
		dtos = append(dtos, h.ollamaAccountToDTO(account))
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
	writeJSON(writer, http.StatusOK, h.ollamaAccountToDTO(account))
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
	// A newly created enabled account is scraped at once instead of waiting out the
	// periodic interval. A created-disabled account stays untouched, and with the
	// collector off nothing is queued at all.
	if account.Enabled {
		h.triggerOllamaRefresh(account.ID)
	}
	writeJSON(writer, http.StatusCreated, h.ollamaAccountToDTO(account))
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
	// The previous state decides whether the update warrants a scrape: a cookie
	// change or a resume does, a label-only edit and a pause do not.
	previous, previousFound, err := h.repo.GetOllamaAccount(request.Context(), id)
	if err != nil {
		writeInternalError(writer, err)
		return
	}
	if !previousFound {
		writeError(writer, http.StatusNotFound, "ollama account not found")
		return
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
	// A new cookie, or a paused account brought back on, is scraped as soon as the
	// current backoff - if any - allows it. A label-only edit sends no request.
	if shouldRefreshOllamaAccount(previous.Enabled, account.Enabled, update.SetCookie) {
		h.triggerOllamaRefresh(account.ID)
	}
	writeJSON(writer, http.StatusOK, h.ollamaAccountToDTO(account))
}

// shouldRefreshOllamaAccount decides whether an update warrants an immediate
// scrape. Only an enabled account is scraped; a resume or a cookie change does, a
// label-only edit or a pause does not. It is a pure function so the truth table is
// tested directly rather than through timing.
func shouldRefreshOllamaAccount(previousEnabled, enabled, cookieChanged bool) bool {
	if !enabled {
		return false
	}
	return cookieChanged || !previousEnabled
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
			dto.Quota = decodeOllamaQuota(account.QuotaSnapshotJSON)
		}
		dtos = append(dtos, dto)
	}
	writeJSON(writer, http.StatusOK, ollamaQuotaResponse{Accounts: dtos})
}

// refreshOllamaAccount runs the console's explicit refresh for one account. It is
// synchronous and bounded by the collector's own timeout, rooted in its service
// context rather than this request: a scrape runs through the shared service
// (honouring a persisted backoff, the manual minimum gap and the in-flight lock) or
// the call coalesces onto the scrape already running and returns its real outcome. A
// suppressed or deadline-limited request answers without pretending success. With the
// collector switched off - or not started yet - it answers 503 and never starts a
// fallback.
func (h *Handler) refreshOllamaAccount(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	id, ok := ollamaAccountID(writer, request)
	if !ok {
		return
	}
	if h.repo == nil {
		writeError(writer, http.StatusServiceUnavailable, "ollama account storage is unavailable")
		return
	}
	if h.ollama == nil {
		writeError(writer, http.StatusServiceUnavailable, "ollama collection is disabled")
		return
	}
	// The collector owns the deadline, rooted in its process context rather than this
	// request: a client disconnect must not truncate a scrape, and the request is never
	// held longer than one refresh timeout. The request context is passed only so a
	// coalesced wait can stop early when the client is gone.
	result, err := h.ollama.RefreshAccount(request.Context(), id)
	if errors.Is(err, ollama.ErrAccountNotFound) {
		writeError(writer, http.StatusNotFound, "ollama account not found")
		return
	}
	if errors.Is(err, ollama.ErrServiceNotStarted) {
		writeError(writer, http.StatusServiceUnavailable, "ollama collection is not running yet")
		return
	}
	if errors.Is(err, ollama.ErrRefreshSuperseded) {
		writeError(writer, http.StatusServiceUnavailable, "ollama refresh did not complete: the account changed while it ran")
		return
	}
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "ollama refresh could not be completed")
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
	response := ollamaRefreshResponse{
		Account:   h.ollamaAccountToDTO(account),
		Refreshed: result.Refreshed,
	}
	if strings.TrimSpace(account.QuotaSnapshotJSON) != "" {
		response.Quota = decodeOllamaQuota(account.QuotaSnapshotJSON)
	}
	if result.Suppressed {
		response.RetryAtMS = result.RetryAtMS
		if response.RetryAtMS == nil {
			fallback := h.now().UnixMilli()
			response.RetryAtMS = &fallback
		}
		h.recordAuditWithoutRequest("ollama.account.refresh", "ollama_account", id, "rate_limited", nil)
		writeJSON(writer, http.StatusTooManyRequests, response)
		return
	}
	h.recordAuditWithoutRequest("ollama.account.refresh", "ollama_account", id, refreshAuditResult(account.Status), nil)
	writeJSON(writer, http.StatusOK, response)
}

// refreshAuditResult names the scrape outcome the audit row records. The request
// itself succeeded whenever it reached here; the result reflects whether the scrape
// produced a reading.
func refreshAuditResult(status string) string {
	if status == ollama.StatusOK {
		return "success"
	}
	return "failure"
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

// ollamaAccountToDTO projects one account's metadata. It adds two live fields the
// console reads but the database does not store: whether a scrape is in flight, and
// when a refresh next becomes allowed (a persisted backoff or the manual gap,
// whichever is later). Neither is a secret.
func (h *Handler) ollamaAccountToDTO(account repository.OllamaAccount) ollamaAccountDTO {
	dto := ollamaAccountDTO{
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
	if h.ollama == nil {
		return dto
	}
	dto.Refreshing = h.ollama.Refreshing(account.ID)
	dto.RetryAtMS = h.ollamaRetryAt(account)
	return dto
}

// ollamaRetryAt is the earliest instant a refresh is allowed again: the persisted
// backoff or the manual gap, whichever is later. It is omitted when neither is
// pending.
func (h *Handler) ollamaRetryAt(account repository.OllamaAccount) *int64 {
	if h.ollama == nil {
		return nil
	}
	nowMS := h.now().UnixMilli()
	var best *int64
	if account.NextAttemptAtMS != nil && *account.NextAttemptAtMS > nowMS {
		best = account.NextAttemptAtMS
	}
	if at, ok := h.ollama.ManualRetryAt(account.ID); ok {
		ms := at.UnixMilli()
		if ms > nowMS && (best == nil || ms > *best) {
			value := ms
			best = &value
		}
	}
	return best
}

func decodeOllamaQuota(encoded string) *quota.NormalizedQuota {
	var value quota.NormalizedQuota
	if err := json.Unmarshal([]byte(encoded), &value); err != nil {
		return nil
	}
	return &value
}

// ollamaDiagnosisDTO is the redacted structure behind one settings-page fetch:
// status, counts, word presence, masked sample lines and same-origin links. It
// carries no page body and no credential.
type ollamaDiagnosisDTO struct {
	Status           int      `json:"status"`
	ContentType      string   `json:"content_type"`
	RedirectLocation string   `json:"redirect_location"`
	BodyBytes        int      `json:"body_bytes"`
	LooksLikeLogin   bool     `json:"looks_like_login"`
	LineCount        int      `json:"line_count"`
	ScriptCount      int      `json:"script_count"`
	PercentCount     int      `json:"percent_count"`
	MoneyCount       int      `json:"money_count"`
	HasSessionWord   bool     `json:"has_session_word"`
	HasWeeklyWord    bool     `json:"has_weekly_word"`
	HasBalanceWord   bool     `json:"has_balance_word"`
	HasCreditWord    bool     `json:"has_credit_word"`
	HasQuotaWord     bool     `json:"has_quota_word"`
	Headings         []string `json:"headings"`
	InterestingLines []string `json:"interesting_lines"`
	HrefPaths        []string `json:"href_paths"`
}

// diagnoseOllamaAccount reports why a stored cookie no longer yields a quota
// reading, without revealing the page or the credential.
//
// The upstream settings page can answer 200 with a live session and still carry
// no parseable window, and the operator cannot share the page because the
// account belongs to someone else. This route therefore performs one bounded
// same-origin fetch and returns only masked structure, so the parser can be
// repaired against evidence instead of guesses. It reads no state, writes none,
// and deliberately does not record an audit entry or a scrape result: a
// diagnostic must not look like a quota observation.
//
// Temporary. Remove this route, its DTO and its tests once the parser tracks the
// current page.
func (h *Handler) diagnoseOllamaAccount(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	id, ok := ollamaAccountID(writer, request)
	if !ok {
		return
	}
	if h.repo == nil {
		writeError(writer, http.StatusServiceUnavailable, "ollama account storage is unavailable")
		return
	}
	if h.cipher == nil {
		writeError(writer, http.StatusServiceUnavailable, "ollama account cipher is unavailable")
		return
	}
	secret, found, err := h.ollamaAccountSecretByID(request.Context(), id)
	if err != nil {
		writeInternalError(writer, err)
		return
	}
	if !found {
		writeError(writer, http.StatusNotFound, "ollama account not found")
		return
	}
	cookie, err := h.cipher.Decrypt(secret.Ciphertext, secret.Nonce)
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "ollama cookie could not be decrypted")
		return
	}
	timeout := h.cfg.Ollama.Timeout
	if timeout <= 0 {
		timeout = ollama.DefaultTimeout
	}
	if timeout > ollamaRefreshTimeout {
		timeout = ollamaRefreshTimeout
	}
	ctx, cancel := context.WithTimeout(request.Context(), timeout)
	defer cancel()
	diagnosis, err := ollama.NewClient(timeout).Diagnose(ctx, string(cookie))
	if err != nil {
		writeError(writer, http.StatusBadGateway, "ollama settings page could not be fetched")
		return
	}
	writeJSON(writer, http.StatusOK, ollamaDiagnosisDTO{
		Status:           diagnosis.Status,
		ContentType:      diagnosis.ContentType,
		RedirectLocation: diagnosis.RedirectLocation,
		BodyBytes:        diagnosis.BodyBytes,
		LooksLikeLogin:   diagnosis.LooksLikeLogin,
		LineCount:        diagnosis.LineCount,
		ScriptCount:      diagnosis.ScriptCount,
		PercentCount:     diagnosis.PercentCount,
		MoneyCount:       diagnosis.MoneyCount,
		HasSessionWord:   diagnosis.HasSessionWord,
		HasWeeklyWord:    diagnosis.HasWeeklyWord,
		HasBalanceWord:   diagnosis.HasBalanceWord,
		HasCreditWord:    diagnosis.HasCreditWord,
		HasQuotaWord:     diagnosis.HasQuotaWord,
		Headings:         diagnosis.Headings,
		InterestingLines: diagnosis.InterestingLines,
		HrefPaths:        diagnosis.HrefPaths,
	})
}
