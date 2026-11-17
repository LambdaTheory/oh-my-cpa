package keyclient

import (
	"strings"
	"testing"
)

// TestJSONObjectPreservesOrderAndUnknownMembers is the property the whole JSON side of
// install rests on: a settings file somebody wrote comes out with its members in the same
// order and its untouched values byte-identical.
func TestJSONObjectPreservesOrderAndUnknownMembers(t *testing.T) {
	original := []byte(`{"zeta": 1, "alpha": {"nested": [1, 2, {"deep": true}]}, "middle": "kept"}`)
	object, err := parseJSONObject(original)
	if err != nil {
		t.Fatal(err)
	}
	if err := object.set("new", "value"); err != nil {
		t.Fatal(err)
	}
	encoded, err := object.encodeIndent()
	if err != nil {
		t.Fatal(err)
	}
	order := []string{`"zeta"`, `"alpha"`, `"middle"`, `"new"`}
	previous := -1
	for _, member := range order {
		index := strings.Index(string(encoded), member)
		if index < previous {
			t.Fatalf("%s moved:\n%s", member, encoded)
		}
		previous = index
	}
	if !strings.Contains(string(encoded), `"new": "value"`) {
		t.Fatalf("the new member is missing:\n%s", encoded)
	}
}

// TestJSONObjectRefusesWhatItCannotModel lists the documents install must not rewrite:
// JSONC, a top-level array, trailing content and duplicate members.
func TestJSONObjectRefusesWhatItCannotModel(t *testing.T) {
	cases := map[string]string{
		"comment":         "{\n  // note\n  \"a\": 1\n}\n",
		"array":           `[1, 2, 3]`,
		"scalar":          `"just a string"`,
		"trailing":        `{"a": 1} {"b": 2}`,
		"garbage":         `{"a": }`,
		"duplicate":       `{"a": 1, "a": 2}`,
		"trailing commas": `{"a": 1,}`,
	}
	for name, content := range cases {
		if _, err := parseJSONObject([]byte(content)); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
}

// TestJSONObjectChildReportsATypeMismatch is the "do not overwrite what you do not
// understand" rule: an env that is a string is somebody's value, not an object to replace.
func TestJSONObjectChildReportsATypeMismatch(t *testing.T) {
	object, err := parseJSONObject([]byte(`{"env": "not-an-object"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := object.child("env"); err == nil {
		t.Fatal("a non-object env was accepted")
	}
	missing, err := object.child("provider")
	if err != nil {
		t.Fatal(err)
	}
	if len(missing.keys) != 0 {
		t.Fatal("a missing member did not produce an empty object")
	}
}

// TestJSONObjectDeleteKeepsTheRest covers the purge side of a JSON edit.
func TestJSONObjectDeleteKeepsTheRest(t *testing.T) {
	object, err := parseJSONObject([]byte(`{"a": 1, "b": {"x": 1, "y": 2}, "c": 3}`))
	if err != nil {
		t.Fatal(err)
	}
	child, err := object.child("b")
	if err != nil {
		t.Fatal(err)
	}
	if !child.delete("x") {
		t.Fatal("delete did not report the member")
	}
	if child.delete("x") {
		t.Fatal("delete reported a member that was already gone")
	}
	if err := object.setChild("b", child); err != nil {
		t.Fatal(err)
	}
	encoded, err := object.encodeIndent()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"b": {`) || !strings.Contains(string(encoded), `"y": 2`) {
		t.Fatalf("the child edit is wrong:\n%s", encoded)
	}
	if strings.Contains(string(encoded), `"x"`) {
		t.Fatalf("the deleted member is still there:\n%s", encoded)
	}
	if !strings.Contains(string(encoded), `"a": 1`) || !strings.Contains(string(encoded), `"c": 3`) {
		t.Fatalf("a sibling disappeared:\n%s", encoded)
	}
}

// TestJSONLeavesFlattenNestedObjects is what the post-write check compares: leaf paths,
// so a nested env entry this tool never touched is still checked.
func TestJSONLeavesFlattenNestedObjects(t *testing.T) {
	object, err := parseJSONObject([]byte(`{"env": {"KEEP": "1", "NESTED": {"deep": true}}, "empty": {}, "top": 2}`))
	if err != nil {
		t.Fatal(err)
	}
	leaves, err := jsonLeaves(object, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"env.KEEP", "env.NESTED.deep", "top"} {
		if _, ok := leaves[path]; !ok {
			t.Fatalf("missing leaf %q in %v", path, leaves)
		}
	}
	if _, ok := leaves["empty"]; ok {
		t.Fatal("an empty object produced a leaf")
	}
}
