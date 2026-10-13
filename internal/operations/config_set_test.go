package operations

import (
	"encoding/json"
	"testing"

	"github.com/oh-my-cpa/oh-my-cpa/internal/capability"
)

// TestConfigSetInputStillAcceptsScalarValues pins that config_set's `value` stays the
// unrestricted schema it is documented as: annotating it must not narrow it, so the
// booleans and integers an allowlisted setting expects still pass input decoding and
// ValidateScalarValue. The JSON wire contract is checked where the tool is serialized,
// in the MCP bridge.
func TestConfigSetInputStillAcceptsScalarValues(t *testing.T) {
	registry := capability.NewRegistry()
	if err := (&Service{}).registerConfig(registry); err != nil {
		t.Fatal(err)
	}
	definition, err := registry.Lookup("config_set", capability.Principal{Adapter: "mcp", IsAdmin: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		`{"key":"force_model_prefix","value":true}`,
		`{"key":"request_retry","value":3}`,
	} {
		if _, err := definition.Validate(json.RawMessage(raw)); err != nil {
			t.Fatalf("schema rejected %s: %v", raw, err)
		}
	}
	for _, input := range []struct {
		key      string
		value    any
		expected any
	}{
		{"force_model_prefix", true, true},
		// The arguments arrive through encoding/json, so an integer is a float64.
		{"request_retry", float64(3), int64(3)},
	} {
		got, err := ValidateScalarValue(input.key, input.value)
		if err != nil || got != input.expected {
			t.Fatalf("ValidateScalarValue(%q, %v) = %v, %v, want %v", input.key, input.value, got, err, input.expected)
		}
	}
}
