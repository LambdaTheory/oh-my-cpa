package automation

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/oh-my-cpa/oh-my-cpa/internal/repository"
)

type missingCredential struct {
	intentVersion int64
	count         int
}

// Unadopt removes only roster membership. It never changes gateway credential status.
func (s *Service) Unadopt(ctx context.Context, authIndex string) (repository.AutomationAccount, error) {
	return s.UnadoptExpected(ctx, authIndex, nil)
}

// UnadoptExpected removes membership from the lifecycle the caller read. A named version that
// no longer matches is a conflict, so a duplicated request cannot unadopt a credential adopted
// again under the same index.
func (s *Service) UnadoptExpected(ctx context.Context, authIndex string, expectedIntentVersion *int64) (repository.AutomationAccount, error) {
	var updated repository.AutomationAccount
	err := s.WithAccount(ctx, authIndex, func(ctx context.Context, account repository.AutomationAccount) error {
		if expectedIntentVersion != nil && *expectedIntentVersion != account.IntentVersion {
			return repository.ErrAutomationConflict
		}
		var err error
		updated, err = s.Repo.RemoveAutomationAccount(ctx, account.AuthIndex, account.IntentVersion, s.now().UnixMilli(), false)
		return err
	})
	return updated, err
}

// UnadoptTarget names one batch member and the lifecycle the caller read for it.
type UnadoptTarget struct {
	AuthIndex string
	// ExpectedIntentVersion is the version the caller's view carried. Zero means the caller
	// named none; only the console always names one.
	ExpectedIntentVersion *int64
}

// ErrStaleBatchMutation is returned by a batch whose members no longer share the lifecycles
// the caller read. No member is unadopted: a batch is all-or-report, so a stale member can
// never be silently dropped while its siblings are acted on.
var ErrStaleBatchMutation = errors.New("automation_batch_stale")

// UnadoptMany treats duplicate and absent selections as no-ops, like adoption.
func (s *Service) UnadoptMany(ctx context.Context, authIndexes []string) (int, error) {
	targets := make([]UnadoptTarget, 0, len(authIndexes))
	for _, authIndex := range authIndexes {
		targets = append(targets, UnadoptTarget{AuthIndex: authIndex})
	}
	removed, err := s.UnadoptManyExpected(ctx, targets)
	if errors.Is(err, ErrStaleBatchMutation) {
		// Legacy callers name no versions, so this cannot happen for them; report it as the
		// generic conflict the HTTP layer already maps for a stale single mutation.
		return removed, repository.ErrAutomationConflict
	}
	return removed, err
}

// UnadoptManyExpected removes a batch of memberships, each against the lifecycle the caller
// read. Every named version is verified under its account lock before any member is removed,
// so a delayed batch cannot split its effect across an old and a new incarnation. The locks
// are taken in sorted order, the same order adoption uses, so the two batches cannot deadlock.
func (s *Service) UnadoptManyExpected(ctx context.Context, targets []UnadoptTarget) (int, error) {
	type selectedTarget struct {
		authIndex string
		expected  *int64
	}
	selected := make([]selectedTarget, 0, len(targets))
	seen := map[string]bool{}
	for _, target := range targets {
		authIndex := strings.TrimSpace(target.AuthIndex)
		if authIndex == "" || seen[authIndex] {
			continue
		}
		seen[authIndex] = true
		selected = append(selected, selectedTarget{authIndex: authIndex, expected: target.ExpectedIntentVersion})
	}
	if len(selected) == 0 {
		return 0, nil
	}
	indexes := make([]string, 0, len(selected))
	for _, target := range selected {
		indexes = append(indexes, target.authIndex)
	}
	sort.Strings(indexes)
	releases := make([]func(), 0, len(indexes))
	for _, authIndex := range indexes {
		releases = append(releases, s.lockAccount(authIndex))
	}
	defer func() {
		for i := len(releases) - 1; i >= 0; i-- {
			releases[i]()
		}
	}()
	// Pass one verifies every version against the stored row. A member that is no longer
	// adopted is a no-op (matching the single path's not-found behavior for an idempotent
	// batch), but a member whose lifecycle advanced is a conflict for the whole batch.
	removable := make(map[string]repository.AutomationAccount, len(selected))
	for _, target := range selected {
		account, found, err := s.Repo.GetAutomationAccount(ctx, target.authIndex)
		if err != nil {
			return 0, err
		}
		if !found || !account.IsAdopted || account.RemovedAtMS != nil {
			continue
		}
		if target.expected != nil && *target.expected != account.IntentVersion {
			return 0, ErrStaleBatchMutation
		}
		removable[target.authIndex] = account
	}
	// Pass two removes each verified member under the intent version just read, which the
	// store checks again inside its own transaction.
	removed := 0
	for _, target := range selected {
		account, ok := removable[target.authIndex]
		if !ok {
			continue
		}
		if _, err := s.Repo.RemoveAutomationAccount(ctx, account.AuthIndex, account.IntentVersion, s.now().UnixMilli(), false); err != nil {
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
