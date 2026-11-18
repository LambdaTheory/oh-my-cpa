package keyclient

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode"
)

const (
	// MODELS_PATH is the OpenAI-compatible model listing a CPA gateway serves. It is
	// appended to the normalized gateway root, which never ends in /v1.
	MODELS_PATH = "/v1/models"

	// MODELS_TIMEOUT bounds the model-list request. A gateway that does not answer in
	// this window is reported as models_unavailable rather than stalling `key exec`
	// before the child starts, and `--models` is the offline way out.
	MODELS_TIMEOUT = 10 * time.Second

	// MAX_MODELS_RESPONSE_BYTES bounds the answer. A model list is a few kilobytes; a
	// larger body is not one, and reading it whole would let a mistaken address fill
	// memory before the shape check can refuse it.
	MAX_MODELS_RESPONSE_BYTES = 1 << 20

	// MAX_MODEL_ID_LENGTH bounds one id. Model ids are short; a longer value is a
	// malformed list or a copy-paste mistake, not a name OpenCode could use.
	MAX_MODEL_ID_LENGTH = 200
)

// modelListResponse is the OpenAI shape `GET /v1/models` answers with.
type modelListResponse struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

// ParseModelList reads the `--models` value: a comma-separated list of model ids.
//
// An empty value means "not given" and returns no list, which is what makes an explicit
// empty string different from an omitted flag only in that it is still empty. A value
// that names no id at all (only commas and spaces) is refused rather than treated as
// "not given": the person asked for a list and did not give one.
func ParseModelList(value string) ([]string, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	parts := strings.Split(value, ",")
	named := 0
	for _, part := range parts {
		if strings.TrimSpace(part) != "" {
			named++
		}
	}
	if named == 0 {
		return nil, Errorf(CODE_MODELS_REQUIRED, "--models names no model id; pass a comma-separated list such as --models gpt-6.2-sol")
	}
	return NormalizeModelIDs(parts)
}

// NormalizeModelIDs trims each id, refuses an empty list, and returns the ids sorted
// and without duplicates.
//
// Sorting makes two installs that saw the same gateway list produce the same bytes, so
// a repeated install is reported as unchanged instead of rewriting the file because the
// gateway answered in another order.
func NormalizeModelIDs(ids []string) ([]string, error) {
	seen := make(map[string]bool, len(ids))
	normalized := make([]string, 0, len(ids))
	for _, raw := range ids {
		id := strings.TrimSpace(raw)
		if id == "" {
			continue
		}
		if err := validateModelID(id); err != nil {
			return nil, err
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		normalized = append(normalized, id)
	}
	if len(normalized) == 0 {
		return nil, Errorf(CODE_MODELS_REQUIRED, "the OpenCode provider needs at least one model id; pass --models a,b,c")
	}
	sort.Strings(normalized)
	return normalized, nil
}

// validateModelID keeps one id from carrying something a JSON member name should not:
// control characters (a newline would make the written file unreadable) and an
// unreasonable length.
func validateModelID(id string) error {
	if len(id) > MAX_MODEL_ID_LENGTH {
		return Errorf(CODE_MODELS_REQUIRED, "the model id is %d characters long, over the %d-character limit", len(id), MAX_MODEL_ID_LENGTH)
	}
	for _, character := range id {
		if unicode.IsControl(character) {
			return Errorf(CODE_MODELS_REQUIRED, "the model id contains a control character")
		}
	}
	return nil
}

// FetchModelIDs reads the model ids the gateway advertises at GET <root>/v1/models,
// authenticated with the repository key.
//
// The key goes in the Authorization header and nowhere else: not in the URL, not in a
// query string, not in an error message. A failure is models_unavailable with the one
// way forward (`--models`), because a provider without a model list is what makes
// OpenCode unusable in the first place.
func FetchModelIDs(ctx context.Context, root, key string) ([]string, error) {
	root = strings.TrimRight(strings.TrimSpace(root), "/")
	if root == "" {
		return nil, Errorf(CODE_BASE_URL_REQUIRED, "no gateway address; pass --base-url, set %s, or save one with `oh-my-cpa key gen --base-url <url>`", ENV_CPA_BASE_URL)
	}
	if strings.TrimSpace(key) == "" {
		return nil, Errorf(CODE_KEY_NOT_FOUND, "no key material; run `oh-my-cpa key gen` and `key accept` first")
	}
	address := root + MODELS_PATH

	requestContext, cancel := context.WithTimeout(ctx, MODELS_TIMEOUT)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodGet, address, nil)
	if err != nil {
		return nil, Wrapf(err, CODE_MODELS_UNAVAILABLE, "cannot build the model-list request for %s: %v; pass --models a,b,c to name the models yourself", address, err)
	}
	request.Header.Set("Authorization", "Bearer "+key)
	request.Header.Set("Accept", "application/json")

	client := &http.Client{}
	response, err := client.Do(request)
	if err != nil {
		return nil, Wrapf(err, CODE_MODELS_UNAVAILABLE, "cannot reach %s to read the model list (%v); pass --models a,b,c to name the models yourself", address, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		// The body is not quoted: a gateway may echo a request id or a partial header,
		// and the status is the part a person acts on.
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
		return nil, Errorf(CODE_MODELS_UNAVAILABLE, "the gateway answered HTTP %d for %s; pass --models a,b,c to name the models yourself", response.StatusCode, address)
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, MAX_MODELS_RESPONSE_BYTES+1))
	if err != nil {
		return nil, Wrapf(err, CODE_MODELS_UNAVAILABLE, "cannot read the model list from %s: %v; pass --models a,b,c to name the models yourself", address, err)
	}
	if len(body) > MAX_MODELS_RESPONSE_BYTES {
		return nil, Errorf(CODE_MODELS_UNAVAILABLE, "%s returned more than %d bytes, which is not a model list; pass --models a,b,c to name the models yourself", address, MAX_MODELS_RESPONSE_BYTES)
	}
	var parsed modelListResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, Wrapf(err, CODE_MODELS_UNAVAILABLE, "%s did not return a JSON model list: %v; pass --models a,b,c to name the models yourself", address, err)
	}
	ids := make([]string, 0, len(parsed.Data))
	for _, entry := range parsed.Data {
		ids = append(ids, entry.ID)
	}
	normalized, normalizeErr := NormalizeModelIDs(ids)
	if normalizeErr != nil {
		return nil, Errorf(CODE_MODELS_UNAVAILABLE, "%s returned no usable model ids; pass --models a,b,c to name the models yourself", address)
	}
	return normalized, nil
}

// modelMembers turns model ids into the members OpenCode expects under
// `provider.<id>.models`: one empty object per model. An empty object leaves every
// model option at OpenCode's defaults, which is what the acceptance run verified against
// a real gateway.
func modelMembers(ids []string) map[string]any {
	members := make(map[string]any, len(ids))
	for _, id := range ids {
		members[id] = map[string]any{}
	}
	return members
}
