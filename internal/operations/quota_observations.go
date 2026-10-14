package operations

import (
	"context"
	"errors"
	"regexp"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/capability"
	"github.com/oh-my-cpa/oh-my-cpa/internal/repository"
)

// QuotaObservationInput narrows a passive-observation read. Every filter is
// optional; the window defaults to the last seven days.
type QuotaObservationInput struct {
	AuthIndex  string `json:"auth_index,omitempty" jsonschema:"Credential auth index; omit for every credential"`
	Provider   string `json:"provider,omitempty" jsonschema:"codex or claude; omit for both"`
	WindowKind string `json:"window_kind,omitempty" jsonschema:"five_hour, weekly, model_scoped or another stored kind; omit for all"`
	Source     string `json:"source,omitempty" jsonschema:"passive (read from request headers) or active (a refresh this deployment performed); omit for both"`
	FromMS     int64  `json:"from_ms,omitempty" jsonschema:"Inclusive epoch milliseconds; defaults to seven days before to_ms"`
	ToMS       int64  `json:"to_ms,omitempty" jsonschema:"Inclusive epoch milliseconds; defaults to now"`
	Limit      int    `json:"limit,omitempty" jsonschema:"Maximum observations to return, 1 to 1000; defaults to 200. Narrow the window instead of paging."`
}

// QuotaObservation is one upstream quota reading at one instant. It is history,
// not a current balance: the same window appears again whenever it moved.
type QuotaObservation struct {
	AuthIndex     string  `json:"auth_index"`
	Provider      string  `json:"provider"`
	WindowKind    string  `json:"window_kind"`
	Scope         string  `json:"scope"`
	Model         string  `json:"model,omitempty"`
	WindowMinutes int     `json:"window_minutes"`
	UsedPercent   float64 `json:"used_percent"`
	ResetAtMS     *int64  `json:"reset_at_ms,omitempty"`
	Status        string  `json:"status"`
	Source        string  `json:"source"`
	ObservedAtMS  int64   `json:"observed_at_ms"`
}

type QuotaObservationList struct {
	Items []QuotaObservation `json:"items"`
}

// quotaObservationMaxDays bounds the queried window. It matches the longest
// retention any window in the stream can have.
const quotaObservationMaxDays = 366

// limitExceeded is a refusal the caller corrects by narrowing its window: the
// result would not fit the requested limit, and silently truncating it would
// read as a complete history.
type limitExceeded struct{ detail string }

func (e limitExceeded) Error() string         { return "tool_result_too_large" }
func (e limitExceeded) FailureDetail() string { return e.detail }

func (s *Service) registerQuotaObservations(registry *capability.Registry) error {
	return read(registry, "quota_observations_list", "Read the quota windows upstreams published on request response headers, oldest first. Each row is one reading at one instant, not a current balance; a window appears again whenever it moved or every fifteen minutes while steady. Filter by credential, provider, window or time window.", func(ctx context.Context, input QuotaObservationInput) (QuotaObservationList, error) {
		filter, err := quotaObservationFilter(ctx, input)
		if err != nil {
			return QuotaObservationList{}, err
		}
		limit, err := quotaObservationLimit(input.Limit)
		if err != nil {
			return QuotaObservationList{}, err
		}
		ctx, cancel := queryContext(ctx)
		defer cancel()
		rows, hasMore, err := s.Repo.ListQuotaObservations(ctx, filter, limit)
		if err != nil {
			return QuotaObservationList{}, err
		}
		if hasMore {
			return QuotaObservationList{}, limitExceeded{detail: "more observations match than the requested limit; narrow from_ms/to_ms or add a filter"}
		}
		output := QuotaObservationList{Items: []QuotaObservation{}}
		for _, row := range rows {
			output.Items = append(output.Items, QuotaObservation{
				AuthIndex: row.AuthIndex, Provider: row.Provider, WindowKind: row.WindowKind,
				Scope: row.Scope, Model: row.Model,
				WindowMinutes: row.WindowMinutes, UsedPercent: row.UsedPercent, ResetAtMS: row.ResetAtMS,
				Status: row.Status, Source: row.Source, ObservedAtMS: row.ObservedAtMS,
			})
		}
		return output, nil
	})
}

// quotaObservationToken bounds a provider, window-kind or source filter to a
// safe identifier: the stored values are provider vocabulary, never free text.
var quotaObservationToken = regexp.MustCompile(`^[a-z0-9_-]{1,64}$`)

func quotaObservationFilter(ctx context.Context, input QuotaObservationInput) (repository.QuotaObservationFilter, error) {
	if input.Provider != "" && !quotaObservationToken.MatchString(input.Provider) {
		return repository.QuotaObservationFilter{}, errors.New("invalid_parameters")
	}
	if input.WindowKind != "" && !quotaObservationToken.MatchString(input.WindowKind) {
		return repository.QuotaObservationFilter{}, errors.New("invalid_parameters")
	}
	if input.Source != "" && input.Source != "passive" && input.Source != "active" {
		return repository.QuotaObservationFilter{}, errors.New("invalid_parameters")
	}
	toMS := input.ToMS
	if toMS == 0 {
		toMS = capability.AnchorMS(ctx)
	}
	fromMS := input.FromMS
	if fromMS == 0 {
		fromMS = toMS - int64(7*24*time.Hour/time.Millisecond)
	}
	if fromMS < 1 || toMS < fromMS || toMS-fromMS > int64(quotaObservationMaxDays*24*time.Hour/time.Millisecond) || toMS > time.Now().Add(time.Minute).UnixMilli() {
		return repository.QuotaObservationFilter{}, errors.New("invalid_window")
	}
	return repository.QuotaObservationFilter{AuthIndex: input.AuthIndex, Provider: input.Provider, WindowKind: input.WindowKind, Source: input.Source, FromMS: fromMS, ToMS: toMS}, nil
}

func quotaObservationLimit(limit int) (int, error) {
	if limit == 0 {
		return 200, nil
	}
	if limit < 1 || limit > 1000 {
		return 0, invalidParameters{detail: "limit must be between 1 and 1000; narrow from_ms/to_ms instead of asking for more"}
	}
	return limit, nil
}
