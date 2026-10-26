package quota

import (
	"context"
	"encoding/json"
	"errors"
	"math"

	"github.com/oh-my-cpa/oh-my-cpa/internal/cpa/management"
)

func parseStepFunBalance(raw []byte) (*QuotaPlan, error) {
	var payload struct {
		Type                string   `json:"type"`
		Balance             *float64 `json:"balance"`
		TotalCashBalance    *float64 `json:"total_cash_balance"`
		TotalVoucherBalance *float64 `json:"total_voucher_balance"`
	}
	// Decode only the documented money fields; never retain an upstream body or
	// quote decoding errors, which can include credential-bearing input.
	if json.Unmarshal(raw, &payload) != nil || payload.Balance == nil ||
		(payload.Type != "prepaid" && payload.Type != "postpaid") {
		return nil, errors.New("invalid stepfun account balance")
	}
	balance := &QuotaAccountBalance{
		AccountType: payload.Type, Currency: "CNY", Balance: *payload.Balance,
		TotalCashBalance: payload.TotalCashBalance, TotalVoucherBalance: payload.TotalVoucherBalance,
	}
	if payload.Type == "prepaid" && payload.TotalCashBalance != nil && payload.TotalVoucherBalance != nil &&
		*payload.TotalCashBalance >= 0 && *payload.TotalVoucherBalance >= 0 {
		// These are lifetime top-ups and gifts, not a recurring allowance. The
		// resulting share is an estimate and must never become a quota window.
		totalFunding := *payload.TotalCashBalance + *payload.TotalVoucherBalance
		if totalFunding > 0 && !math.IsInf(totalFunding, 0) {
			remaining := clamp(*payload.Balance/totalFunding, 0, 1) * 100
			balance.RemainingPercent = &remaining
		}
	}
	return &QuotaPlan{PlanType: payload.Type, Tier: "unknown", AccountBalance: balance}, nil
}

func (s *Service) fetchStepFunBalance(ctx context.Context, file management.AuthFile) (*QuotaPlan, error) {
	headers := management.WithQuotaCredential(map[string]string{"Accept": "application/json"})
	response, err := s.SafeApiCall(ctx, file.AuthIndex, "GET", StepFunAccountsURL, headers, "")
	if err != nil {
		return nil, err
	}
	body, err := response.NormalizedBody()
	if err != nil {
		return nil, errors.New("invalid stepfun account response")
	}
	return parseStepFunBalance(body)
}
