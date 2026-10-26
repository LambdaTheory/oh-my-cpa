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

// syncRoster confirms absence against the inventory.
//
// A missing credential leaves the active roster after two consecutive complete,
// successful, authoritative inventories under the same intent version. Pending recovery,
// retry, an automation state or a saved reset does not protect the row: none of them can
// execute for a credential the gateway no longer has, and keeping the plan would leave a
// placeholder that is not a current credential. The counter belongs to this process and
// intent version: a restart, an untrusted inventory, a reappearance or a changed operator
// intent requires two fresh confirmations. Removal itself is the repository's transaction,
// which snapshots the plan before the tombstone and sends nothing to the gateway.
func (s *Service) syncRoster(ctx context.Context, accounts []repository.AutomationAccount, inventory Inventory) {
	next := map[string]missingCredential{}
	for _, account := range accounts {
		if inventory.Present(account.AuthIndex) {
			continue
		}
		if !account.IsAdopted || account.RemovedAtMS != nil {
			continue
		}
		err := s.WithAccount(ctx, account.AuthIndex, func(ctx context.Context, fresh repository.AutomationAccount) error {
			if inventory.Present(fresh.AuthIndex) {
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
