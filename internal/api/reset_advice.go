package api

import "github.com/oh-my-cpa/oh-my-cpa/internal/operations"

type resetAdviceDTO struct {
	Verdict           string                       `json:"verdict"`
	Evidence          string                       `json:"evidence"`
	Reasons           []string                     `json:"reasons"`
	RemainingResets   *int                         `json:"remaining_resets"`
	EarliestExpiryMS  *int64                       `json:"earliest_expiry_ms"`
	UsedPercent       *float64                     `json:"used_percent"`
	ResetObservedAtMS int64                        `json:"reset_observed_at_ms"`
	QuotaObservedAtMS int64                        `json:"quota_observed_at_ms"`
	LocalEvidence     resetAdviceLocalDTO          `json:"local_evidence"`
	Announcements     []resetAdviceAnnouncementDTO `json:"announcements"`
}

type resetAdviceLocalDTO struct {
	Source         string   `json:"source"`
	Verdict        string   `json:"verdict"`
	Evidence       string   `json:"evidence"`
	Reasons        []string `json:"reasons"`
	QuotaResetAtMS *int64   `json:"quota_reset_at_ms"`
}

type resetAdviceAnnouncementDTO struct {
	ID                string `json:"id"`
	Source            string `json:"source"`
	Scope             string `json:"scope"`
	Model             string `json:"model,omitempty"`
	Confidence        string `json:"confidence"`
	ExpectedResetAtMS *int64 `json:"expected_reset_at_ms"`
	RegisteredAtMS    int64  `json:"registered_at_ms"`
	InfluencesAdvice  bool   `json:"influences_advice"`
	HasConflict       bool   `json:"has_conflict"`
}

func projectResetAdvice(item operations.ResetAdviceItem) *resetAdviceDTO {
	result := &resetAdviceDTO{Verdict: item.Verdict, Evidence: item.Evidence, Reasons: item.Reasons, RemainingResets: item.RemainingResets, EarliestExpiryMS: item.EarliestExpiryMS, UsedPercent: item.UsedPercent, ResetObservedAtMS: item.ResetObservedAtMS, QuotaObservedAtMS: item.QuotaObservedAtMS, LocalEvidence: resetAdviceLocalDTO{Source: item.LocalEvidence.Source, Verdict: item.LocalEvidence.Verdict, Evidence: item.LocalEvidence.Evidence, Reasons: item.LocalEvidence.Reasons, QuotaResetAtMS: item.LocalEvidence.QuotaResetAtMS}, Announcements: []resetAdviceAnnouncementDTO{}}
	for _, entry := range item.Announcements {
		result.Announcements = append(result.Announcements, resetAdviceAnnouncementDTO{ID: entry.ID, Source: entry.Source, Scope: entry.Scope, Model: entry.Model, Confidence: entry.Confidence, ExpectedResetAtMS: entry.ExpectedResetAtMS, RegisteredAtMS: entry.RegisteredAtMS, InfluencesAdvice: entry.InfluencesAdvice, HasConflict: entry.HasConflict})
	}
	return result
}
