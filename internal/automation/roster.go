package automation

import (
	"context"
	"errors"
	"strings"

	"github.com/oh-my-cpa/oh-my-cpa/internal/repository"
)

type missingCredential struct {
	intentVersion int64
	count         int
}

// Unadopt removes only roster membership. It never changes gateway credential status.
func (s *Service) Unadopt(ctx context.Context, authIndex string) (repository.AutomationAccount, error) {
	var updated repository.AutomationAccount
	err := s.WithAccount(ctx, authIndex, func(ctx context.Context, account repository.AutomationAccount) error {
		var err error
		updated, err = s.Repo.RemoveAutomationAccount(ctx, account.AuthIndex, account.IntentVersion, s.now().UnixMilli(), false)
		return err
	})
	return updated, err
}

// UnadoptMany treats duplicate and absent selections as no-ops, like adoption.
func (s *Service) UnadoptMany(ctx context.Context, authIndexes []string) (int, error) {
	removed := 0
	seen := map[string]bool{}
	for _, authIndex := range authIndexes {
		authIndex = strings.TrimSpace(authIndex)
		if authIndex == "" || seen[authIndex] {
			continue
		}
		seen[authIndex] = true
		if _, err := s.Unadopt(ctx, authIndex); errors.Is(err, ErrAccountNotFound) {
			continue
		} else if err != nil {
			return removed, err
		}
		removed++
	}
	return removed, nil
}

func canRemoveMissingAccount(account repository.AutomationAccount) bool {
	return account.NextAttemptAtMS == nil && account.State != StateDisabled && account.State != StateWaiting &&
		account.CycleResetAtMS == nil && (account.ManualResetAtMS == nil || account.ManualResetConsumed)
}

// The counter belongs to this process and intent version. A restart, degraded read,
// gateway reappearance or changed operator intent requires two fresh confirmations.
func (s *Service) syncRoster(ctx context.Context, accounts []repository.AutomationAccount, credentials map[string]Credential) {
	next := map[string]missingCredential{}
	for _, account := range accounts {
		if _, exists := credentials[account.AuthIndex]; exists {
			continue
		}
		if !account.IsAdopted || !canRemoveMissingAccount(account) {
			continue
		}
		err := s.WithAccount(ctx, account.AuthIndex, func(ctx context.Context, fresh repository.AutomationAccount) error {
			if !canRemoveMissingAccount(fresh) {
				return nil
			}
			previous := s.missing[fresh.AuthIndex]
			count := 1
			if previous.intentVersion == fresh.IntentVersion {
				count += previous.count
			}
			if count < 2 {
				next[fresh.AuthIndex] = missingCredential{intentVersion: fresh.IntentVersion, count: count}
				return nil
			}
			_, err := s.Repo.RemoveAutomationAccount(ctx, fresh.AuthIndex, fresh.IntentVersion, s.now().UnixMilli(), true)
			return err
		})
		if err != nil && !errors.Is(err, ErrAccountNotFound) {
			s.logWrite("auto_remove", account.AuthIndex, err)
		}
	}
	s.missing = next
}
