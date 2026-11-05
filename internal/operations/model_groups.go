package operations

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"sort"

	"github.com/oh-my-cpa/oh-my-cpa/internal/capability"
	"github.com/oh-my-cpa/oh-my-cpa/internal/repository"
)

// Model groups are the operator's ordered answer to "which models are for this
// kind of work". The console writes them; these two capabilities are the only
// reader an external agent has, and they are deliberately read-only: a group is
// a human judgement, not something a caller should be able to reshape mid-run.
//
// No second availability rule lives here: each member takes the verdict
// ProjectModelAdvice produces from the same directory, credentials and quota
// readings, so the same model cannot be `available` in a group and `avoid` in
// model advice. Only the ordering differs: advice sorts by burn
// rate, a group sorts by availability band and then by the operator's order.

const (
	// modelGroupsGroupLimit and modelGroupsMemberLimit bound one query. A group
	// is a hand-curated list, so a hundred of either is already far past a
	// useful one, and the capability's result budget is shared by every group
	// query.
	modelGroupsGroupLimit  = 100
	modelGroupsMemberLimit = 100
	modelGroupsByteLimit   = 32 * 1024
)

const (
	// ModelGroupAvailabilityAvailable is a model model_advice would call ok.
	ModelGroupAvailabilityAvailable = "available"
	// ModelGroupAvailabilityTight is usable but nearly spent (advice: caution).
	ModelGroupAvailabilityTight = "tight"
	// ModelGroupAvailabilityUnknown is no usable evidence (advice: unknown).
	ModelGroupAvailabilityUnknown = "unknown"
	// ModelGroupAvailabilityUnavailable is spent or blocked for every account
	// that serves it (advice: avoid). It is still returned, last.
	ModelGroupAvailabilityUnavailable = "unavailable"
)

const (
	modelGroupNotFoundReason = "model_group_not_found"
	modelGroupEmptyReason    = "model_group_empty"
)

// modelGroupAvailabilityRanking orders the bands the PRD fixed: usable, nearly
// spent, unknown, unusable. "unknown" sits above "unavailable" because an
// unreadable quota may still be usable, while an exhausted one certainly is not.
var modelGroupAvailabilityRanking = map[string]int{
	ModelGroupAvailabilityAvailable:   0,
	ModelGroupAvailabilityTight:       1,
	ModelGroupAvailabilityUnknown:     2,
	ModelGroupAvailabilityUnavailable: 3,
}

type ModelGroupsListInput struct{}

// ModelGroupSummary is the whole of what listing groups reveals: a name and a
// size. Member names and quota detail belong to the per-group read.
type ModelGroupSummary struct {
	Name        string `json:"name"`
	MemberCount int    `json:"member_count"`
}

type ModelGroupsListResult struct {
	Groups  []ModelGroupSummary `json:"groups"`
	Omitted int                 `json:"omitted"`
}

type ModelGroupsModelsInput struct {
	Group string `json:"group" jsonschema:"Model group name exactly as the operator named it; after a rename the old name is unknown"`
}

// ModelGroupModelItem is one member as a caller needs it: the client-requested
// name, the availability band, the recorded balance, and where it sat in
// the operator's order. No account, credential or provider identity is carried.
type ModelGroupModelItem struct {
	Model        string             `json:"model"`
	Availability string             `json:"availability"`
	Balance      *ModelGroupBalance `json:"balance"`
	Position     int                `json:"position"`
	IsPreferred  bool               `json:"is_preferred"`
}

type ModelGroupsModelsResult struct {
	Group   string                `json:"group"`
	IsFound bool                  `json:"is_found"`
	Reason  string                `json:"reason,omitempty"`
	Models  []ModelGroupModelItem `json:"models"`
	Omitted int                   `json:"omitted"`
}

// ModelGroupReading is one model's console-facing state. It carries exactly the two
// figures model_groups_models reports for a member - the availability band and the
// balance - because the console and a caller must never disagree about the same name.
// The console needs them for names that are not in a group yet as well: the add page's
// candidates come from the gateway directory, so a reading is not a group property.
type ModelGroupReading struct {
	Model        string             `json:"model"`
	Availability string             `json:"availability"`
	Balance      *ModelGroupBalance `json:"balance"`
}

// ModelGroupReadingsResult covers every name the group pages can render: the members of
// all groups plus the models the gateway advertises. Ordering is by name so a repeated
// read of unchanged data is byte-identical.
type ModelGroupReadingsResult struct {
	Models []ModelGroupReading `json:"models"`
}

func (s *Service) registerModelGroups(registry *capability.Registry) error {
	if err := read(registry, "model_groups_list", "List the operator's model groups with each group's name and member count. Read-only; group names are what the per-group read accepts. Member names and quota detail come from model_groups_models.", func(ctx context.Context, _ ModelGroupsListInput) (ModelGroupsListResult, error) {
		if s.Repo == nil {
			return ModelGroupsListResult{}, errors.New("capability_unavailable")
		}
		groups, err := s.Repo.ListModelGroups(ctx)
		if err != nil {
			return ModelGroupsListResult{}, err
		}
		result := ModelGroupsListResult{Groups: []ModelGroupSummary{}}
		for _, group := range groups {
			if len(result.Groups) == modelGroupsGroupLimit {
				result.Omitted++
				continue
			}
			result.Groups = append(result.Groups, ModelGroupSummary{Name: group.Name, MemberCount: len(group.Members)})
		}
		return result, nil
	}); err != nil {
		return err
	}
	return read(registry, "model_groups_models", "Read one operator-defined model group: every member in availability order (available, tight, unknown, unavailable) and then in the operator's order, each with its availability, recorded USD balance and group position. Availability uses model_advice's verdicts. Unusable models are returned last, never filtered out. Read-only; use the name from model_groups_list.", s.modelGroupModels)
}

func (s *Service) modelGroupModels(ctx context.Context, input ModelGroupsModelsInput) (ModelGroupsModelsResult, error) {
	if s.Repo == nil {
		return ModelGroupsModelsResult{}, errors.New("capability_unavailable")
	}
	group, err := s.Repo.GetModelGroupByName(ctx, input.Group)
	if errors.Is(err, repository.ErrModelGroupNotFound) {
		return ModelGroupsModelsResult{IsFound: false, Reason: modelGroupNotFoundReason, Models: []ModelGroupModelItem{}}, nil
	}
	if err != nil {
		return ModelGroupsModelsResult{}, err
	}
	result := ModelGroupsModelsResult{Group: group.Name, IsFound: true, Models: []ModelGroupModelItem{}}
	if len(group.Members) == 0 {
		result.Reason = modelGroupEmptyReason
		return result, nil
	}

	members := group.Members
	if len(members) > modelGroupsMemberLimit {
		result.Omitted = len(members) - modelGroupsMemberLimit
		members = members[:modelGroupsMemberLimit]
	}
	names := make([]string, 0, len(members))
	for _, member := range members {
		names = append(names, member.Model)
	}
	nowMS := capability.AnchorMS(ctx)
	directory, credentials, quotas := s.modelGroupAdviceInputs(ctx)
	advice := projectModelGroupAdvice(names, directory, quotas, credentials, nowMS, s.AdviceBlocked)
	balances := modelGroupBalances(names, directory, credentials, quotas)
	result.Models = projectModelGroupModels(members, advice, balances)
	boundModelGroupsModels(&result)
	return result, nil
}

// ModelGroupReadings is the console's read of the same projection. It resolves every
// name the group pages may draw - the members of all groups and the models the gateway
// advertises, which are the add page's candidates - through projectModelGroupAdvice and
// modelGroupBalances, the functions model_groups_models itself uses. One projection is
// the point: a member cannot be available in the console and unavailable to a caller.
//
// Unreadable evidence is not an error here either. modelGroupAdviceInputs degrades a
// failed directory, credential or quota read to empty evidence, so the names still come
// back with availability "unknown" and a null balance, exactly as the per-group read
// answers when quota data is missing.
func (s *Service) ModelGroupReadings(ctx context.Context) (ModelGroupReadingsResult, error) {
	if s.Repo == nil {
		return ModelGroupReadingsResult{}, errors.New("capability_unavailable")
	}
	groups, err := s.Repo.ListModelGroups(ctx)
	if err != nil {
		return ModelGroupReadingsResult{}, err
	}
	names := make([]string, 0, len(groups))
	seen := make(map[string]bool)
	collect := func(name string) {
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		names = append(names, name)
	}
	for _, group := range groups {
		for _, member := range group.Members {
			collect(member.Model)
		}
	}
	directory, credentials, quotas := s.modelGroupAdviceInputs(ctx)
	for _, model := range directory.Models {
		collect(model.ID)
	}
	sort.Strings(names)
	advice := projectModelGroupAdvice(names, directory, quotas, credentials, capability.AnchorMS(ctx), s.AdviceBlocked)
	balances := modelGroupBalances(names, directory, credentials, quotas)
	return ModelGroupReadingsResult{Models: projectModelGroupReadings(names, advice, balances)}, nil
}

func projectModelGroupReadings(names []string, advice ModelAdviceResult, balances map[string]*ModelGroupBalance) []ModelGroupReading {
	verdicts := make(map[string]string, len(advice.Models))
	for _, item := range advice.Models {
		verdicts[item.Model] = item.Verdict
	}
	readings := make([]ModelGroupReading, 0, len(names))
	for _, name := range names {
		// A name with no verdict is one the projection could not measure; the empty
		// verdict maps to "unknown" for the same reason a missing reading does.
		readings = append(readings, ModelGroupReading{
			Model:        name,
			Availability: modelGroupAvailability(verdicts[name]),
			Balance:      balances[name],
		})
	}
	return readings
}

// projectModelGroupAdvice asks ProjectModelAdvice for one member at a time.
// That projection bounds its own result to model_advice's 50-row / 32 KiB
// capability budget; a single-member call cannot reach that bound, so a group
// of up to modelGroupsMemberLimit keeps, for every member, the verdict
// model_advice would give that model on its own.
func projectModelGroupAdvice(models []string, directory ModelSquareDirectory, quotas []Quota, credentials []AdviceCredential, nowMS int64, isBlocked func(string, string) bool) ModelAdviceResult {
	result := ModelAdviceResult{Models: []ModelAdviceItem{}, GeneratedAtMS: nowMS}
	for _, model := range models {
		single := ProjectModelAdvice(ModelAdviceInput{Models: []string{model}}, directory, quotas, credentials, nowMS, isBlocked)
		result.Models = append(result.Models, single.Models...)
	}
	return result
}

// modelGroupAdviceInputs reads the same three sources model_advice uses and
// degrades a failure into empty evidence instead of a failed call: the PRD
// requires the member list even when quota data cannot be read at all, with
// every availability then reported as unknown.
func (s *Service) modelGroupAdviceInputs(ctx context.Context) (ModelSquareDirectory, []AdviceCredential, []Quota) {
	if s.AdviceDirectory == nil || s.AdviceCredentials == nil || s.Quotas == nil {
		return ModelSquareDirectory{}, nil, nil
	}
	directory, err := s.AdviceDirectory(ctx)
	if err != nil {
		return ModelSquareDirectory{}, nil, nil
	}
	credentials, err := s.AdviceCredentials(ctx)
	if err != nil {
		return directory, nil, nil
	}
	quotas, err := s.readAdviceQuotas(ctx)
	if err != nil {
		return directory, credentials, nil
	}
	return directory, credentials, quotas
}

// projectModelGroupModels keeps the group's own vocabulary while borrowing the
// verdicts: it maps advice's four outcomes onto the group's four bands, then
// orders by band, operator position and finally the client-requested name so
// two reads of unchanged data return the same list.
func projectModelGroupModels(members []repository.ModelGroupMember, advice ModelAdviceResult, balances map[string]*ModelGroupBalance) []ModelGroupModelItem {
	verdicts := make(map[string]string, len(advice.Models))
	for _, item := range advice.Models {
		verdicts[item.Model] = item.Verdict
	}
	items := make([]ModelGroupModelItem, 0, len(members))
	for index, member := range members {
		position := member.Position
		if position <= 0 {
			position = index + 1
		}
		items = append(items, ModelGroupModelItem{
			Model:        member.Model,
			Availability: modelGroupAvailability(verdicts[member.Model]),
			Balance:      balances[member.Model],
			Position:     position,
			IsPreferred:  position == 1,
		})
	}
	sort.SliceStable(items, func(left, right int) bool {
		first, second := items[left], items[right]
		if modelGroupAvailabilityRanking[first.Availability] != modelGroupAvailabilityRanking[second.Availability] {
			return modelGroupAvailabilityRanking[first.Availability] < modelGroupAvailabilityRanking[second.Availability]
		}
		if first.Position != second.Position {
			return first.Position < second.Position
		}
		return first.Model < second.Model
	})
	return items
}

func modelGroupAvailability(verdict string) string {
	switch verdict {
	case "ok":
		return ModelGroupAvailabilityAvailable
	case "caution":
		return ModelGroupAvailabilityTight
	case "avoid":
		return ModelGroupAvailabilityUnavailable
	}
	return ModelGroupAvailabilityUnknown
}

func boundModelGroupsModels(result *ModelGroupsModelsResult) {
	for len(result.Models) > 0 {
		encoded, _ := json.Marshal(*result)
		if len(encoded) <= modelGroupsByteLimit {
			return
		}
		result.Models = result.Models[:len(result.Models)-1]
		result.Omitted++
	}
}

// modelGroupUpstream resolves the one upstream a client-requested name routes
// to. A name with no route or several upstream targets has no defensible
// per-model balance; the same ambiguity keeps model_advice unknown.
func modelGroupUpstream(routes []ModelSquareRoute) (string, bool) {
	targets := map[string]bool{}
	for _, route := range routes {
		targets[route.UpstreamModel] = true
	}
	if len(routes) == 0 || targets[""] || len(targets) != 1 {
		return "", false
	}
	return routes[0].UpstreamModel, true
}

// modelGroupCredentialServes mirrors ProjectModelAdvice's credential matching,
// so balances are drawn from the accounts the verdict was drawn from.
func modelGroupCredentialServes(model, upstream string, routes []ModelSquareRoute, providers map[string]ModelSquareProvider, credential AdviceCredential) bool {
	if credential.IsDisabled {
		return false
	}
	hasRoute := false
	for _, route := range routes {
		provider := providers[route.ProviderID]
		if route.ProviderID == "" || provider.ID == "" {
			continue
		}
		if credential.ProviderID != "" {
			hasRoute = credential.ProviderID == route.ProviderID
		} else {
			hasRoute = provider.IsOAuth && credential.Provider == provider.Family
		}
		if hasRoute {
			break
		}
	}
	if !hasRoute || isModelExcluded(upstream, credential.ExcludedModels) {
		return false
	}
	for _, served := range credential.Models {
		if served == model || served == upstream {
			return true
		}
	}
	return len(credential.Models) == 0
}

// ModelGroupBalance preserves the upstream decimal precision of monetary evidence.
type ModelGroupBalance struct {
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
}

// modelGroupBalances counts each serving account once and ignores missing balances.
func modelGroupBalances(models []string, directory ModelSquareDirectory, credentials []AdviceCredential, quotas []Quota) map[string]*ModelGroupBalance {
	balances := make(map[string]*ModelGroupBalance, len(models))
	routes := map[string][]ModelSquareRoute{}
	for _, route := range directory.Routes {
		routes[route.CallPoint] = append(routes[route.CallPoint], route)
	}
	providers := map[string]ModelSquareProvider{}
	for _, provider := range directory.Providers {
		providers[provider.ID] = provider
	}
	readings := map[string]Quota{}
	for _, reading := range quotas {
		readings[reading.AuthIndex] = reading
	}
	for _, model := range models {
		upstream, resolved := modelGroupUpstream(routes[model])
		if !resolved {
			continue
		}
		total := new(big.Rat)
		precision := 0
		hasBalance := false
		seen := map[string]bool{}
		for _, credential := range credentials {
			if seen[credential.AuthIndex] || !modelGroupCredentialServes(model, upstream, routes[model], providers, credential) {
				continue
			}
			seen[credential.AuthIndex] = true
			balance := readings[credential.AuthIndex].Balance
			if balance == nil {
				continue
			}
			amount, scale, ok := parseGroupBalance(balance.Amount)
			if !ok {
				continue
			}
			total.Add(total, amount)
			if scale > precision {
				precision = scale
			}
			hasBalance = true
		}
		if hasBalance {
			balances[model] = &ModelGroupBalance{Amount: total.FloatString(precision), Currency: "USD"}
		}
	}
	return balances
}

func parseGroupBalance(amount string) (*big.Rat, int, bool) {
	if amount == "" {
		return nil, 0, false
	}
	point := -1
	for index, char := range amount {
		if char == '.' && point == -1 && index > 0 && index < len(amount)-1 {
			point = index
			continue
		}
		if char < '0' || char > '9' {
			return nil, 0, false
		}
	}
	scale := 0
	if point >= 0 {
		scale = len(amount) - point - 1
	}
	value, ok := new(big.Rat).SetString(amount)
	return value, scale, ok
}
