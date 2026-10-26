package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/oh-my-cpa/oh-my-cpa/internal/capability"
	appcrypto "github.com/oh-my-cpa/oh-my-cpa/internal/crypto"
	"github.com/oh-my-cpa/oh-my-cpa/internal/feishu"
	"github.com/oh-my-cpa/oh-my-cpa/internal/operations"
	"github.com/oh-my-cpa/oh-my-cpa/internal/repository"
)

// Feishu notification API (lane C of the first batch).
//
// The routes are session-authenticated and same-origin guarded exactly like every
// other management write; the read-only MCP key reaches none of them. The DTOs are
// a deliberate allowlist: they carry a has_secret flag and no secret, and the app
// secret is accepted on write only. The secret never appears in DTOs, logs, errors
// or audit details.
//
// The service is not wired into the shared handler here: the integration owner
// builds one FeishuAPI and registers these routes on the authenticated v1 router,
// then starts and stops the service with the rest of the background loops.

const (
	feishuRequestLimit = 16 << 10
	feishuAppIDLimit   = 128
	feishuSecretLimit  = 512
	feishuChatIDLimit  = 128
	feishuTimeLimit    = 5
)

// errFeishuCipherUnavailable is returned when a secret write is attempted without a
// configured master-key cipher. It carries no secret material.
var errFeishuCipherUnavailable = errors.New("feishu: master-key cipher is not configured")

// FeishuAPI owns the Feishu service and its settings routes.
type FeishuAPI struct {
	handler    *Handler
	repo       *repository.Repository
	cipher     *appcrypto.Cipher
	logger     *slog.Logger
	now        func() time.Time
	service    *feishu.Service
	prediction *feishu.PredictionJob
	// overlay is the environment override. It is retained so a settings save
	// re-applies the same precedence (an explicit environment off still wins).
	overlay feishu.Overlay
}

// Service exposes the built bot so the integration owner can start and stop it with
// the other background loops.
func (f *FeishuAPI) Service() *feishu.Service { return f.service }

func (f *FeishuAPI) PredictionJob() *feishu.PredictionJob { return f.prediction }

// BuildFeishuAPI builds the bot and its routes. source supplies the read-only
// report the bot answers from; the application passes
// Handler.BuildFeishuReportSource, and a nil source makes queries answer "no data"
// rather than failing. envOverlay is the environment override supplied by the
// config layer and may be zero.
//
// A missing repository or an unusable configuration never fails the caller: an
// unusable configuration yields an inert, degraded service.
func (h *Handler) BuildFeishuAPI(ctx context.Context, source feishu.ReportSource, envOverlay feishu.Overlay) (*FeishuAPI, error) {
	api := &FeishuAPI{
		handler: h,
		repo:    h.repo,
		cipher:  h.cipher,
		logger:  h.logger,
		now:     h.now,
		overlay: envOverlay,
	}
	if h.repo == nil {
		// Without storage the module is inert; the routes answer 503 rather than
		// crashing the process.
		api.service = feishu.NewService(feishu.Options{
			Logger: h.logger,
			Now:    h.now,
			Source: source,
		})
		return api, nil
	}
	web, err := loadFeishuWebConfig(ctx, h.repo)
	if err != nil {
		return nil, err
	}
	overlay, err := api.overlayWithStoredSecret(ctx)
	if err != nil {
		return nil, err
	}
	api.service = feishu.NewService(feishu.Options{
		ConsoleURL: feishu.ConsoleAddress(h.cfg.PublicURL, h.cfg.BasePath),
		Web:        web, Overlay: overlay, Store: feishuStore{repo: h.repo}, Source: source,
		Logger: h.logger, Now: h.now,
	})
	h.buildReauthService(api.service)
	api.prediction = feishu.NewPredictionJob(api.service, api.readPrediction)
	return api, nil
}

func (f *FeishuAPI) readPrediction(ctx context.Context) (feishu.PredictionDigest, error) {
	prediction, err := f.handler.operationsService().QuotaPrediction(capability.WithAnchor(ctx, f.now().UnixMilli()), operations.QuotaPredictionInput{FindingsOnly: true, IncludeRecovery: true})
	if err != nil {
		return feishu.PredictionDigest{}, err
	}
	items := make([]feishu.PredictionDigestItem, 0, len(prediction.Items))
	for _, item := range prediction.Items {
		items = append(items, feishu.PredictionDigestItem{
			CredentialLabel: item.CredentialLabel, Provider: item.Provider, WindowKind: item.WindowKind,
			UsedPercent: item.UsedPercent, ProjectedPercentAtReset: item.ProjectedPercentAtReset,
			ProjectedTokensAtReset: item.ProjectedTokensAtReset, ProjectedCostNanosAtReset: item.ProjectedCostNanosAtReset,
			ObservedAtMS: item.ObservedAtMS, Verdict: item.Verdict,
			ExhaustionETAMS: item.ExhaustionETAMS, WindowScope: item.WindowScope,
			Key: item.AuthIndex, WindowID: item.WindowID, ResetAtMS: item.ResetAtMS, ResetAccuracy: item.ResetAccuracy,
		})
	}
	return feishu.PredictionDigest{Items: items, Omitted: prediction.Omitted}, nil
}

// RegisterFeishuRoutes installs the Feishu routes on the authenticated v1 router.
// It is called inside the /api/v1 group in handler.go when the bot has been built.
func RegisterFeishuRoutes(router chi.Router, api *FeishuAPI) {
	router.Get("/feishu/settings", api.getSettings)
	router.Put("/feishu/settings", api.putSettings)
	router.Get("/feishu/status", api.getStatus)
	router.Post("/feishu/test", api.postTest)
	router.Post("/feishu/prediction-test", api.postPredictionTest)
}

type feishuSettingsDTO struct {
	Enabled               bool     `json:"enabled"`
	AppID                 string   `json:"app_id,omitempty"`
	HasSecret             bool     `json:"has_secret"`
	ChatID                string   `json:"chat_id,omitempty"`
	SummaryTimes          []string `json:"summary_times"`
	Timezone              string   `json:"timezone,omitempty"`
	QuietStart            string   `json:"quiet_start"`
	QuietEnd              string   `json:"quiet_end"`
	TightRemainingPercent float64  `json:"tight_remaining_percent"`
	ChannelUnavailableMS  int64    `json:"channel_unavailable_after_ms"`
	MonitorDownMS         int64    `json:"monitor_down_after_ms"`
	Source                string   `json:"source"`
	Degraded              bool     `json:"degraded"`
	Reason                string   `json:"reason,omitempty"`
	ForcedOff             bool     `json:"forced_off"`
	UpdatedAtMS           *int64   `json:"updated_at_ms,omitempty"`
}

type feishuSettingsWriteDTO struct {
	Enabled               *bool    `json:"enabled"`
	AppID                 *string  `json:"app_id"`
	AppSecret             *string  `json:"app_secret"`
	ChatID                *string  `json:"chat_id"`
	SummaryTimes          []string `json:"summary_times"`
	Timezone              *string  `json:"timezone"`
	QuietStart            *string  `json:"quiet_start"`
	QuietEnd              *string  `json:"quiet_end"`
	TightRemainingPercent *float64 `json:"tight_remaining_percent"`
	ChannelUnavailableMS  *int64   `json:"channel_unavailable_after_ms"`
	MonitorDownMS         *int64   `json:"monitor_down_after_ms"`
}

type feishuTestResponse struct {
	Attempts      int        `json:"attempts,omitempty"`
	NextAttemptAt *time.Time `json:"next_attempt_at,omitempty"`
	Sent          bool       `json:"sent"`
	Reason        string     `json:"reason,omitempty"`
	Error         string     `json:"error,omitempty"`
}

func (f *FeishuAPI) getSettings(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	if f.repo == nil {
		writeError(writer, http.StatusServiceUnavailable, "feishu storage is unavailable")
		return
	}
	settings, found, err := f.repo.LoadFeishuSettings(request.Context())
	if err != nil {
		writeInternalError(writer, err)
		return
	}
	_, _, hasSecret, err := f.repo.LoadFeishuSecret(request.Context())
	if err != nil {
		writeInternalError(writer, err)
		return
	}
	status := f.service.StatusSnapshot(request.Context())
	dto := feishuSettingsFrom(settings, found, hasSecret, status)
	writeJSON(writer, http.StatusOK, dto)
}

func (f *FeishuAPI) putSettings(writer http.ResponseWriter, request *http.Request) {
	if f.repo == nil {
		writeError(writer, http.StatusServiceUnavailable, "feishu storage is unavailable")
		return
	}
	var payload feishuSettingsWriteDTO
	if err := decodeManagementJSON(writer, request, feishuRequestLimit, &payload); err != nil {
		return
	}
	settings, found, err := f.repo.LoadFeishuSettings(request.Context())
	if err != nil {
		writeInternalError(writer, err)
		return
	}
	if !found {
		settings = repository.FeishuSettings{
			SummaryTimesJSON:        `["10:00","17:00"]`,
			QuietStart:              "23:00",
			QuietEnd:                "08:00",
			TightRemainingPercent:   10,
			ChannelUnavailableAfter: 30 * 60 * 1000,
			MonitorDownAfterMS:      30 * 60 * 1000,
		}
	}
	if err := applyFeishuWrite(&settings, payload); err != nil {
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	}
	settings.UpdatedAtMS = f.now().UnixMilli()
	if err := f.repo.SaveFeishuSettings(request.Context(), settings); err != nil {
		writeInternalError(writer, err)
		return
	}
	if payload.AppSecret != nil {
		if err := f.saveSecret(request.Context(), *payload.AppSecret); err != nil {
			f.writeFeishuCipherError(writer, err)
			return
		}
	}
	web := feishuWebConfig(settings)
	overlay, err := f.overlayWithStoredSecret(request.Context())
	if err != nil {
		writeInternalError(writer, err)
		return
	}
	f.service.Reload(request.Context(), web, overlay)
	f.recordAudit(request, "feishu.settings.update", "feishu", "settings", "success", map[string]any{
		"secret_changed": payload.AppSecret != nil,
	})
	_, _, hasSecret, _ := f.repo.LoadFeishuSecret(request.Context())
	status := f.service.StatusSnapshot(request.Context())
	writeJSON(writer, http.StatusOK, feishuSettingsFrom(settings, true, hasSecret, status))
}

func (f *FeishuAPI) getStatus(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	writeJSON(writer, http.StatusOK, f.service.StatusSnapshot(request.Context()))
}

// postTest sends a live status card to the configured group. It is reached only by
// an explicit operator action; production testing is the operator's own choice and
// the bot never triggers it on its own.
func (f *FeishuAPI) postTest(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	delivery, err := f.service.SendTest(request.Context())
	if err != nil {
		f.recordAudit(request, "feishu.test", "feishu", "settings", "failure", nil)
		writeJSON(writer, http.StatusOK, feishuTestResponse{Sent: false, Error: err.Error()})
		return
	}
	response := feishuTestResponse{Sent: delivery.Sent, Attempts: delivery.Attempts}
	if delivery.QueuedForRetry {
		response.Reason = "queued_for_retry"
		response.NextAttemptAt = &delivery.NextAttemptAt
	}
	f.recordAudit(request, "feishu.test", "feishu", "settings", "success", map[string]any{"sent": delivery.Sent, "reason": response.Reason, "attempts": delivery.Attempts})
	writeJSON(writer, http.StatusOK, response)
}

func (f *FeishuAPI) postPredictionTest(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	if f.prediction == nil {
		writeJSON(writer, http.StatusOK, feishuTestResponse{Sent: false, Error: "prediction sender is unavailable"})
		return
	}
	sent, err := f.prediction.SendNow(request.Context())
	if err != nil {
		f.recordAudit(request, "feishu.prediction.send", "feishu", "prediction", "failure", nil)
		writeJSON(writer, http.StatusOK, feishuTestResponse{Sent: false, Error: err.Error()})
		return
	}
	if !sent {
		f.recordAudit(request, "feishu.prediction.send", "feishu", "prediction", "success", map[string]any{"sent": false, "reason": "no_findings"})
		writeJSON(writer, http.StatusOK, feishuTestResponse{Sent: false, Reason: "no_findings"})
		return
	}
	f.recordAudit(request, "feishu.prediction.send", "feishu", "prediction", "success", map[string]any{"sent": true})
	writeJSON(writer, http.StatusOK, feishuTestResponse{Sent: true})
}

// saveSecret encrypts and stores the app secret.
func (f *FeishuAPI) saveSecret(ctx context.Context, secret string) error {
	if f.cipher == nil {
		return errFeishuCipherUnavailable
	}
	ciphertext, nonce, err := f.cipher.Encrypt([]byte(secret))
	if err != nil {
		return err
	}
	return f.repo.SaveFeishuSecret(ctx, ciphertext, nonce)
}

func (f *FeishuAPI) writeFeishuCipherError(writer http.ResponseWriter, err error) {
	if f.logger != nil {
		f.logger.Warn("feishu app secret could not be encrypted", "error", err)
	}
	writeError(writer, http.StatusServiceUnavailable, "master key is not configured; the secret cannot be stored")
}

// overlayWithStoredSecret returns the environment overlay with the web-stored
// secret filled in when the environment did not override it.
func (f *FeishuAPI) overlayWithStoredSecret(ctx context.Context) (feishu.Overlay, error) {
	overlay := f.overlay
	if overlay.AppSecret != nil || f.repo == nil || f.cipher == nil {
		return overlay, nil
	}
	ciphertext, nonce, has, err := f.repo.LoadFeishuSecret(ctx)
	if err != nil {
		return overlay, err
	}
	if !has {
		return overlay, nil
	}
	plaintext, err := f.cipher.Decrypt(ciphertext, nonce)
	if err != nil {
		return overlay, err
	}
	secret := string(plaintext)
	overlay.AppSecret = &secret
	return overlay, nil
}

// recordAudit writes an audit line through the owning handler when one is present.
func (f *FeishuAPI) recordAudit(request *http.Request, action, targetType, targetID, result string, details map[string]any) {
	if f.handler == nil {
		return
	}
	_ = f.handler.recordAudit(request, action, targetType, targetID, result, details)
}

func loadFeishuWebConfig(ctx context.Context, repo *repository.Repository) (feishu.Config, error) {
	settings, found, err := repo.LoadFeishuSettings(ctx)
	if err != nil {
		return feishu.Config{}, err
	}
	if !found {
		return feishu.Config{}, nil
	}
	return feishuWebConfig(settings), nil
}

func feishuWebConfig(settings repository.FeishuSettings) feishu.Config {
	var times []string
	if strings.TrimSpace(settings.SummaryTimesJSON) != "" {
		_ = json.Unmarshal([]byte(settings.SummaryTimesJSON), &times)
	}
	return feishu.Config{
		Enabled:                 settings.Enabled,
		AppID:                   settings.AppID,
		ChatID:                  settings.ChatID,
		SummaryTimes:            times,
		Timezone:                settings.Timezone,
		QuietStart:              settings.QuietStart,
		QuietEnd:                settings.QuietEnd,
		TightRemainingPercent:   feishu.DefaultTightRemainingPercent,
		ChannelUnavailableAfter: time.Duration(settings.ChannelUnavailableAfter) * time.Millisecond,
		MonitorDownAfter:        time.Duration(settings.MonitorDownAfterMS) * time.Millisecond,
	}
}

func feishuSettingsFrom(settings repository.FeishuSettings, found, hasSecret bool, status feishu.Status) feishuSettingsDTO {
	dto := feishuSettingsDTO{
		SummaryTimes:          []string{"10:00", "17:00"},
		QuietStart:            "23:00",
		QuietEnd:              "08:00",
		TightRemainingPercent: 10,
		ChannelUnavailableMS:  30 * 60 * 1000,
		MonitorDownMS:         30 * 60 * 1000,
		HasSecret:             hasSecret,
		Source:                status.Source,
		Degraded:              status.Degraded,
		Reason:                status.Reason,
		ForcedOff:             status.ForcedOff,
	}
	if !found {
		return dto
	}
	dto.Enabled = settings.Enabled
	dto.AppID = settings.AppID
	dto.ChatID = settings.ChatID
	dto.Timezone = settings.Timezone
	dto.QuietStart = settings.QuietStart
	dto.QuietEnd = settings.QuietEnd
	dto.TightRemainingPercent = feishu.DefaultTightRemainingPercent
	dto.ChannelUnavailableMS = settings.ChannelUnavailableAfter
	dto.MonitorDownMS = settings.MonitorDownAfterMS
	if settings.SummaryTimesJSON != "" {
		var times []string
		if json.Unmarshal([]byte(settings.SummaryTimesJSON), &times) == nil && len(times) > 0 {
			dto.SummaryTimes = times
		}
	}
	if settings.UpdatedAtMS > 0 {
		updated := settings.UpdatedAtMS
		dto.UpdatedAtMS = &updated
	}
	return dto
}

// applyFeishuWrite validates and merges a settings write onto the stored values.
func applyFeishuWrite(settings *repository.FeishuSettings, payload feishuSettingsWriteDTO) error {
	if payload.Enabled != nil {
		settings.Enabled = *payload.Enabled
	}
	if payload.AppID != nil {
		value := strings.TrimSpace(*payload.AppID)
		if len(value) > feishuAppIDLimit {
			return errors.New("app_id is too long")
		}
		settings.AppID = value
	}
	if payload.ChatID != nil {
		value := strings.TrimSpace(*payload.ChatID)
		if len(value) > feishuChatIDLimit {
			return errors.New("chat_id is too long")
		}
		settings.ChatID = value
	}
	if payload.AppSecret != nil {
		if strings.TrimSpace(*payload.AppSecret) == "" {
			return errors.New("app_secret must not be empty")
		}
		if len(*payload.AppSecret) > feishuSecretLimit {
			return errors.New("app_secret is too long")
		}
	}
	if payload.SummaryTimes != nil {
		if len(payload.SummaryTimes) > feishuTimeLimit {
			return errors.New("too many summary times")
		}
		normalized, err := normalizeTimeList(payload.SummaryTimes)
		if err != nil {
			return err
		}
		settings.SummaryTimesJSON = marshalStringList(normalized)
	}
	if payload.Timezone != nil {
		settings.Timezone = strings.TrimSpace(*payload.Timezone)
	}
	if payload.QuietStart != nil {
		value, err := normalizeClock(*payload.QuietStart, true)
		if err != nil {
			return err
		}
		settings.QuietStart = value
	}
	if payload.QuietEnd != nil {
		value, err := normalizeClock(*payload.QuietEnd, true)
		if err != nil {
			return err
		}
		settings.QuietEnd = value
	}
	if payload.TightRemainingPercent != nil {
		value := *payload.TightRemainingPercent
		if value != feishu.DefaultTightRemainingPercent {
			return errors.New("tight_remaining_percent is read-only and fixed at 10")
		}
		settings.TightRemainingPercent = feishu.DefaultTightRemainingPercent
	}
	if payload.ChannelUnavailableMS != nil {
		if *payload.ChannelUnavailableMS < 0 {
			return errors.New("channel_unavailable_after_ms must not be negative")
		}
		settings.ChannelUnavailableAfter = *payload.ChannelUnavailableMS
	}
	if payload.MonitorDownMS != nil {
		if *payload.MonitorDownMS < 0 {
			return errors.New("monitor_down_after_ms must not be negative")
		}
		settings.MonitorDownAfterMS = *payload.MonitorDownMS
	}
	return nil
}

func normalizeTimeList(values []string) ([]string, error) {
	out := make([]string, 0, len(values))
	for _, value := range values {
		normalized, err := normalizeClock(value, false)
		if err != nil {
			return nil, errors.New("summary_times entries must be HH:MM")
		}
		out = append(out, normalized)
	}
	return out, nil
}

// normalizeClock validates an "HH:MM" value and returns its canonical form. emptyOK
// allows an empty value for a field that has a default.
func normalizeClock(value string, emptyOK bool) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		if emptyOK {
			return "", nil
		}
		return "", errors.New("time is required")
	}
	parsed, err := time.Parse("15:04", value)
	if err != nil {
		return "", errors.New("time must be HH:MM")
	}
	return parsed.Format("15:04"), nil
}

func marshalStringList(values []string) string {
	encoded, err := json.Marshal(values)
	if err != nil {
		return "[]"
	}
	return string(encoded)
}

// feishuStore adapts the repository to the feishu.Store interface, marshalling the
// module payloads to and from the opaque JSON the repository stores.
type feishuStore struct {
	repo *repository.Repository
}

// Load implements feishu.Store.
func (s feishuStore) Load(ctx context.Context) (*feishu.State, error) {
	runtime, err := s.repo.LoadFeishuRuntime(ctx)
	if err != nil {
		return nil, err
	}
	return feishuStateFrom(runtime), nil
}

// Save implements feishu.Store.
func (s feishuStore) Save(ctx context.Context, state *feishu.State) error {
	if state == nil {
		return nil
	}
	return s.repo.SaveFeishuRuntime(ctx, feishuRuntimeFrom(state))
}

func feishuStateFrom(runtime repository.FeishuRuntime) *feishu.State {
	state := feishu.EmptyState()
	state.Bootstrapped = runtime.Bootstrapped
	if runtime.LastSummaryAtMS != nil {
		state.LastSummaryAt = time.UnixMilli(*runtime.LastSummaryAtMS).UTC()
	}
	for _, item := range runtime.Pending {
		state.PendingItems = append(state.PendingItems, feishu.PendingItem{
			ID:           item.ID,
			Kind:         item.Kind,
			Provider:     item.Provider,
			Key:          item.CredKey,
			Alias:        item.Alias,
			Since:        timeOfMS(item.SinceMS),
			LastSeen:     timeOfMS(item.LastSeenMS),
			Action:       item.Action,
			Status:       item.Status,
			NotifiedAt:   timeOfMS(item.NotifiedAtMS),
			ResolvedAt:   timeOfMS(item.ResolvedAtMS),
			CycleResetAt: timeOfMS(item.CycleResetAtMS),
		})
	}
	for _, notification := range runtime.Notifications {
		state.Outbox = append(state.Outbox, feishuNotificationFrom(notification))
	}
	for _, track := range runtime.Tracks {
		state.ChannelTracks[track.Provider] = feishu.ChannelTrack{
			FirstSeen: timeOfMS(track.FirstSeenMS),
			LastValid: timeOfMS(track.LastValidMS),
		}
	}
	return state
}

func feishuRuntimeFrom(state *feishu.State) repository.FeishuRuntime {
	runtime := repository.FeishuRuntime{
		Bootstrapped: state.Bootstrapped,
	}
	if !state.LastSummaryAt.IsZero() {
		ms := state.LastSummaryAt.UnixMilli()
		runtime.LastSummaryAtMS = &ms
	}
	for _, item := range state.PendingItems {
		runtime.Pending = append(runtime.Pending, repository.FeishuPendingItem{
			ID:             item.ID,
			Kind:           item.Kind,
			Provider:       item.Provider,
			CredKey:        item.Key,
			Alias:          item.Alias,
			SinceMS:        msOf(item.Since),
			LastSeenMS:     msOf(item.LastSeen),
			Action:         item.Action,
			Status:         item.Status,
			NotifiedAtMS:   msOf(item.NotifiedAt),
			ResolvedAtMS:   msOf(item.ResolvedAt),
			CycleResetAtMS: msOf(item.CycleResetAt),
		})
	}
	for _, notification := range state.Outbox {
		runtime.Notifications = append(runtime.Notifications, repository.FeishuNotification{
			ID:              notification.ID,
			Kind:            notification.Kind,
			ItemIDsJSON:     marshalJSON(notification.ItemIDs),
			AlertsJSON:      marshalJSON(notification.Alerts),
			PendingJSON:     marshalJSON(notification.Pending),
			TightJSON:       marshalJSON(notification.Tight),
			State:           notification.State,
			Attempts:        notification.Attempts,
			NextAttemptAtMS: msOf(notification.NextAttemptAt),
			SentAtMS:        msOf(notification.SentAt),
			OccurredAtMS:    msOf(notification.OccurredAt),
			CreatedAtMS:     notification.CreatedAt.UnixMilli(),
		})
	}
	providers := make([]string, 0, len(state.ChannelTracks))
	for provider := range state.ChannelTracks {
		providers = append(providers, provider)
	}
	sort.Strings(providers)
	for _, provider := range providers {
		track := state.ChannelTracks[provider]
		runtime.Tracks = append(runtime.Tracks, repository.FeishuChannelTrack{
			Provider:    provider,
			FirstSeenMS: msOf(track.FirstSeen),
			LastValidMS: msOf(track.LastValid),
		})
	}
	return runtime
}

func feishuNotificationFrom(notification repository.FeishuNotification) feishu.Notification {
	out := feishu.Notification{
		ID:            notification.ID,
		Kind:          notification.Kind,
		State:         notification.State,
		Attempts:      notification.Attempts,
		NextAttemptAt: timeOfMS(notification.NextAttemptAtMS),
		SentAt:        timeOfMS(notification.SentAtMS),
		OccurredAt:    timeOfMS(notification.OccurredAtMS),
		CreatedAt:     time.UnixMilli(notification.CreatedAtMS).UTC(),
	}
	_ = json.Unmarshal([]byte(notification.ItemIDsJSON), &out.ItemIDs)
	_ = json.Unmarshal([]byte(notification.AlertsJSON), &out.Alerts)
	_ = json.Unmarshal([]byte(notification.PendingJSON), &out.Pending)
	_ = json.Unmarshal([]byte(notification.TightJSON), &out.Tight)
	return out
}

func marshalJSON(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "[]"
	}
	return string(encoded)
}

func msOf(value time.Time) *int64 {
	if value.IsZero() {
		return nil
	}
	ms := value.UnixMilli()
	return &ms
}

func timeOfMS(value *int64) time.Time {
	if value == nil || *value <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(*value).UTC()
}
