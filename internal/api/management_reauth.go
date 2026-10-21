package api

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/oh-my-cpa/oh-my-cpa/internal/feishu"
	"github.com/oh-my-cpa/oh-my-cpa/internal/quota"
	"github.com/oh-my-cpa/oh-my-cpa/internal/reauth"
	"github.com/oh-my-cpa/oh-my-cpa/internal/repository"
)

func (h *Handler) buildReauthService(bot *feishu.Service) {
	if h.cfg.IsDemoMode {
		return
	}
	h.reauth = reauth.New(reauth.Options{Repo: h.repo, Now: h.now, Logger: h.logger, Enabled: bot.OAuthReady, Notify: bot.SendOAuthCard, Refresh: h.refreshReauthCredential})
	bot.ConfigureOAuth(h.cfg.Feishu.OAuthChatID, h.cfg.Feishu.OAuthContactOpenID, h.reauth.AcceptCode)
}
func (h *Handler) RunReauth(ctx context.Context) error {
	if h.reauth == nil {
		return nil
	}
	return h.reauth.Run(ctx)
}
func (h *Handler) refreshReauthCredential(ctx context.Context, authIndex string) (string, error) {
	client, err := h.capabilityClient(ctx)
	if err != nil {
		return "unknown", err
	}
	files, err := client.AuthFiles(ctx)
	if err != nil {
		return "unknown", err
	}
	found := -1
	for i, file := range files.Files {
		if strings.TrimSpace(file.AuthIndex) == authIndex {
			if found != -1 {
				return "unknown", errors.New("ambiguous_credential")
			}
			found = i
		}
	}
	if found < 0 {
		return "unknown", sql.ErrNoRows
	}
	file := files.Files[found]
	if _, ok := reauth.CredentialProvider(file.Type, file.Provider, file.AccountType); !ok {
		return "unknown", errors.New("provider_changed")
	}
	q, err := h.readCredentialQuota(ctx, quota.NewService(client), file, h.loadPriorNormalizedQuota(ctx, authIndex))
	if err != nil {
		return "unknown", err
	}
	if err := h.persistNormalizedQuotaSnapshot(ctx, q); err != nil {
		return "unknown", err
	}
	return classifyQuotaSignal(q), nil
}

type reauthJobDTO struct {
	Flow            string `json:"flow"`
	CodeKind        string `json:"code_kind"`
	UserCode        string `json:"user_code,omitempty"`
	VerificationURL string `json:"verification_url,omitempty"`
	ExpiresAtMS     int64  `json:"expires_at_ms,omitempty"`
	ID              string `json:"id"`
	AuthIndex       string `json:"auth_index"`
	Provider        string `json:"provider"`
	AccountLabel    string `json:"account_label"`
	State           string `json:"state"`
	Attempts        int    `json:"attempts"`
	CreatedAtMS     int64  `json:"created_at_ms"`
	UpdatedAtMS     int64  `json:"updated_at_ms"`
	LastError       string `json:"last_error"`
}

func reauthDTO(j repository.ReauthJob) reauthJobDTO {
	return reauthJobDTO{Flow: j.Flow, CodeKind: j.CodeKind, UserCode: j.UserCode, VerificationURL: j.VerificationURL, ExpiresAtMS: j.ExpiresAtMS, ID: j.ID, AuthIndex: j.AuthIndex, Provider: j.Provider, AccountLabel: j.AccountLabel, State: j.State, Attempts: j.Attempts, CreatedAtMS: j.CreatedAtMS, UpdatedAtMS: j.UpdatedAtMS, LastError: j.LastError}
}
func (h *Handler) registerReauthRoutes(router chi.Router) {
	router.Route("/management/reauth/jobs", func(r chi.Router) {
		r.Use(h.requireReauthRunner)
		r.Get("/next", h.nextReauthJob)
		r.Get("/{id}", h.getReauthJob)
		r.Get("/{id}/code", h.takeReauthCode)
		r.Post("/{id}/status", h.reportReauthStatus)
	})
}
func (h *Handler) requireReauthRunner(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		ip := net.ParseIP(host)
		allowed := false
		if ip != nil && (ip.IsLoopback() || ip.IsPrivate()) {
			for _, cidr := range h.cfg.Feishu.ReauthRunnerCIDRs {
				_, network, err := net.ParseCIDR(cidr)
				if err == nil && network.Contains(ip) {
					allowed = true
					break
				}
			}
		}
		// Forwarding headers are never evidence of local runner identity.
		if !allowed || r.Header.Get("Forwarded") != "" || r.Header.Get("X-Forwarded-For") != "" {
			writeError(w, http.StatusForbidden, "reauth_runner_not_allowed")
			return
		}
		if h.cfg.IsDemoMode || !h.reauth.Enabled() {
			writeError(w, http.StatusServiceUnavailable, "reauth_disabled")
			return
		}
		if !sameOrigin(r) {
			writeError(w, http.StatusForbidden, "same_origin_required")
			return
		}
		next.ServeHTTP(w, r)
	})
}
func writeReauthError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writeError(w, 404, "reauth_job_not_found")
	case errors.Is(err, reauth.ErrConflict):
		writeError(w, 409, "reauth_state_conflict")
	case errors.Is(err, reauth.ErrDisabled):
		writeError(w, 503, "reauth_disabled")
	default:
		writeError(w, 503, "reauth_unavailable")
	}
}
func (h *Handler) nextReauthJob(w http.ResponseWriter, r *http.Request) {
	provider, ok := reauth.ProviderFor(r.URL.Query().Get("provider"))
	if !ok {
		writeError(w, 400, "unsupported_provider")
		return
	}
	j, err := h.reauth.Next(r.Context(), provider.ID)
	if errors.Is(err, sql.ErrNoRows) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err != nil {
		writeReauthError(w, err)
		return
	}
	writeJSON(w, 200, reauthDTO(j))
}
func (h *Handler) getReauthJob(w http.ResponseWriter, r *http.Request) {
	j, err := h.reauth.Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeReauthError(w, err)
		return
	}
	writeJSON(w, 200, reauthDTO(j))
}
func (h *Handler) takeReauthCode(w http.ResponseWriter, r *http.Request) {
	code, err := h.reauth.TakeCode(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeReauthError(w, err)
		return
	}
	if code == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, 200, struct {
		Code string `json:"code"`
	}{Code: code})
}
func (h *Handler) reportReauthStatus(w http.ResponseWriter, r *http.Request) {
	var input struct {
		reauth.OAuthStart
		Status    string `json:"status"`
		LastError string `json:"last_error"`
	}
	if !readAgentInput(w, r, &input) {
		return
	}
	if input.Status != "failed" && input.LastError != "" {
		writeError(w, 400, "invalid_parameters")
		return
	}
	if err := h.reauth.Report(r.Context(), chi.URLParam(r, "id"), input.Status, input.LastError, input.OAuthStart); err != nil {
		writeReauthError(w, err)
		return
	}
	h.getReauthJob(w, r)
}
