package automation

import (
	"errors"
	"testing"
)

func TestValidateInventoryRejectsAmbiguousIdentitiesBeforeMapping(t *testing.T) {
	for name, credentials := range map[string][]Credential{
		"blank index":     {{AuthIndex: " ", Name: "a.json", Controllable: true}},
		"malformed index": {{AuthIndex: "idx one", Name: "a.json", Controllable: true}},
		"control index":   {{AuthIndex: "idx\x00one", Name: "a.json", Controllable: true}},
		"duplicate index": {
			{AuthIndex: "idx-1", Name: "a.json", Controllable: true},
			{AuthIndex: " idx-1 ", Name: "b.json", Controllable: true},
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ValidateInventory(credentials); !errors.Is(err, ErrInventoryIncomplete) {
				t.Fatalf("inventory = %v, want ErrInventoryIncomplete", err)
			}
		})
	}
}

func TestValidateInventoryKeepsACompleteEmptyListAndAPresentUnnamedCredential(t *testing.T) {
	empty, err := ValidateInventory(nil)
	if err != nil || len(empty.Ordered) != 0 || len(empty.ByIndex) != 0 {
		t.Fatalf("a complete empty inventory must validate: %+v %v", empty, err)
	}

	inventory, err := ValidateInventory([]Credential{{AuthIndex: "idx-1", Provider: "codex", Controllable: true}})
	if err != nil {
		t.Fatal(err)
	}
	credential, present := inventory.ByIndex["idx-1"]
	if !present {
		t.Fatal("a credential without a name is present, not absent")
	}
	if credential.Controllable {
		t.Fatalf("a credential without a name must not be writable: %+v", credential)
	}
}
