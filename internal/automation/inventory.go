package automation

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// Complete credential inventories.
//
// The gateway's auth-file list is the only evidence a credential currently exists.
// Every path that turns that list into a decision - the background pass, adoption and
// the console's read projection - goes through ValidateInventory first, so a partial or
// ambiguous response can never be read as "the credential is gone" by one path while
// another still sees it.
//
// CPA's /credentials response has no total, page or completeness field in the contract
// OMC targets. A missing or null files array is the only incompleteness signal OMC can
// decode; an unmarked truncation cannot be detected here and is documented as a limit
// (see ADR 0116).

// ErrInventoryUnavailable marks a credential list OMC could not read at all. It is
// never evidence of absence.
var ErrInventoryUnavailable = errors.New("credential_inventory_unavailable")

// ErrInventoryIncomplete marks a credential list that was read but cannot be trusted as
// complete: a blank or malformed auth index, or two entries claiming one index. The
// duplicate case is exactly what a roster/inventory count mismatch looks like from the
// inside, so it degrades the whole inventory rather than picking one arbitrarily.
var ErrInventoryIncomplete = errors.New("credential_inventory_incomplete")

// maxAuthIndexRunes bounds an auth index. CPA keys are opaque, so the bound is only a
// guard against a malformed payload, not a format.
const maxAuthIndexRunes = 256

// Inventory is one validated credential list. ByIndex is the only lookup a caller may
// use: it exists only after every identity passed validation, so a missing key is a
// real absence rather than a decoding accident.
type Inventory struct {
	Ordered []Credential
	ByIndex map[string]Credential
}

// ValidateInventory trims and checks every credential before constructing a map. A
// credential whose name is missing stays in the inventory - it is present but not
// writable - because "present without a name" is the opposite of absence. An empty,
// complete list is valid and means the gateway really reports no credentials.
func ValidateInventory(credentials []Credential) (Inventory, error) {
	inventory := Inventory{
		Ordered: make([]Credential, 0, len(credentials)),
		ByIndex: make(map[string]Credential, len(credentials)),
	}
	for _, credential := range credentials {
		authIndex := strings.TrimSpace(credential.AuthIndex)
		if err := validateAuthIndex(authIndex); err != nil {
			return Inventory{}, fmt.Errorf("%w: %v", ErrInventoryIncomplete, err)
		}
		if _, duplicate := inventory.ByIndex[authIndex]; duplicate {
			return Inventory{}, fmt.Errorf("%w: duplicate auth index", ErrInventoryIncomplete)
		}
		credential.AuthIndex = authIndex
		credential.Name = strings.TrimSpace(credential.Name)
		credential.Provider = strings.TrimSpace(credential.Provider)
		credential.AccountLabel = strings.TrimSpace(credential.AccountLabel)
		// A name is what CPA's status patch is addressed by. A credential without one
		// is present but cannot be written, whatever an adapter claimed.
		credential.Controllable = credential.Controllable && credential.Name != ""
		inventory.Ordered = append(inventory.Ordered, credential)
		inventory.ByIndex[authIndex] = credential
	}
	return inventory, nil
}

func validateAuthIndex(authIndex string) error {
	if authIndex == "" {
		return errors.New("blank auth index")
	}
	if len([]rune(authIndex)) > maxAuthIndexRunes {
		return errors.New("auth index too long")
	}
	if strings.IndexFunc(authIndex, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return errors.New("malformed auth index")
	}
	return nil
}

// Present reports whether the validated inventory contains a credential under the
// given auth index. It is the only presence test automatic actions use.
func (i Inventory) Present(authIndex string) bool {
	_, ok := i.ByIndex[strings.TrimSpace(authIndex)]
	return ok
}

// indexes lists the validated auth indexes, for one evidence query per pass.
func (i Inventory) indexes() []string {
	result := make([]string, 0, len(i.Ordered))
	for _, credential := range i.Ordered {
		result = append(result, credential.AuthIndex)
	}
	return result
}
