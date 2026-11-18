package keyclient

import (
	"strings"
	"testing"
)

// TestCodexBlockIsAddedReplacedAndRemovedOnce covers the whole lifecycle of the managed
// region: no duplicate block on reinstall, and a purge that leaves the original file.
func TestCodexBlockIsAddedReplacedAndRemovedOnce(t *testing.T) {
	original := "# keep this comment\nmodel = \"gpt-5\"\n\n[model_providers.other]\nname = \"Other\"\n"
	record := RepoState{Repo: "org/repo"}

	document, err := parseTOML([]byte(original))
	if err != nil {
		t.Fatal(err)
	}
	document.appendBlock(codexManagedBlock(record, "http://127.0.0.1:8317", "sk-cpa-one"))
	if err := document.setTopLevelKey(CODEX_MODEL_PROVIDER, CODEX_PROVIDER_ID); err != nil {
		t.Fatal(err)
	}
	first := string(document.encode())
	if strings.Count(first, CODEX_BLOCK_START) != 1 {
		t.Fatalf("the block appears %d times:\n%s", strings.Count(first, CODEX_BLOCK_START), first)
	}
	if !strings.HasPrefix(first, "# keep this comment\nmodel = \"gpt-5\"\nmodel_provider = \"oh-my-cpa\"\n") {
		t.Fatalf("the top-level keys are wrong:\n%s", first)
	}
	if !strings.Contains(first, "[model_providers.other]\nname = \"Other\"\n") {
		t.Fatalf("the other table changed:\n%s", first)
	}

	// A second install replaces the block in place rather than stacking a second one.
	again, err := parseTOML([]byte(first))
	if err != nil {
		t.Fatal(err)
	}
	start, end, found, err := again.blockMarkerPositions()
	if err != nil || !found {
		t.Fatalf("the block was not found: %v", err)
	}
	again.removeBlock(start, end)
	again.appendBlock(codexManagedBlock(record, "http://127.0.0.1:8317", "sk-cpa-two"))
	if err := again.setTopLevelKey(CODEX_MODEL_PROVIDER, CODEX_PROVIDER_ID); err != nil {
		t.Fatal(err)
	}
	second := string(again.encode())
	if strings.Count(second, CODEX_BLOCK_START) != 1 {
		t.Fatalf("the reinstall stacked a block:\n%s", second)
	}
	if strings.Count(second, "sk-cpa-two") != 1 || strings.Contains(second, "sk-cpa-one") {
		t.Fatalf("the block was not replaced:\n%s", second)
	}

	// Removal restores the original file exactly, including the blank line.
	removed, err := parseTOML([]byte(second))
	if err != nil {
		t.Fatal(err)
	}
	start, end, found, err = removed.blockMarkerPositions()
	if err != nil || !found {
		t.Fatalf("the block was not found: %v", err)
	}
	removed.removeBlock(start, end)
	if err := removed.deleteTopLevelKey(CODEX_MODEL_PROVIDER); err != nil {
		t.Fatal(err)
	}
	if got := string(removed.encode()); got != original {
		t.Fatalf("removal did not restore the file:\n---got---\n%s\n---want---\n%s", got, original)
	}
}

// TestCodexBlockRestoresThePreviousProvider is the "give back what was there" rule.
func TestCodexBlockRestoresThePreviousProvider(t *testing.T) {
	previous := "openai"
	slot := RuntimeSlot{PreviousModelProvider: &previous}
	content := "model_provider = \"oh-my-cpa\"\n\n" + CODEX_BLOCK_START + "\n[model_providers.oh-my-cpa]\nname = \"x\"\n" + CODEX_BLOCK_END + "\n"
	encoded, changed, err := removeCodexEdit([]byte(content), slot)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("nothing was removed")
	}
	if string(encoded) != "model_provider = \"openai\"\n" {
		t.Fatalf("the previous provider was not restored:\n%s", encoded)
	}
}

// TestCodexBlockLeavesASubsequentChoiceAlone is the boundary of that rule: somebody who
// picked another provider after the install keeps it.
func TestCodexBlockLeavesASubsequentChoiceAlone(t *testing.T) {
	content := "model_provider = \"lmstudio\"\n\n" + CODEX_BLOCK_START + "\n[model_providers.oh-my-cpa]\nname = \"x\"\n" + CODEX_BLOCK_END + "\n"
	encoded, _, err := removeCodexEdit([]byte(content), RuntimeSlot{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `model_provider = "lmstudio"`) {
		t.Fatalf("the later choice was overwritten:\n%s", encoded)
	}
	if strings.Contains(string(encoded), CODEX_BLOCK_START) {
		t.Fatalf("the block survived:\n%s", encoded)
	}
}

// TestCodexBlockRefusesUnpairedMarkers is the interrupted-write case on the removal side:
// deleting to EOF would take the operator's whole file with it.
func TestCodexBlockRefusesUnpairedMarkers(t *testing.T) {
	cases := map[string]string{
		"start only": CODEX_BLOCK_START + "\n[model_providers.oh-my-cpa]\nname = \"x\"\n",
		"end only":   CODEX_BLOCK_END + "\n",
		"twice":      CODEX_BLOCK_START + "\n" + CODEX_BLOCK_END + "\n" + CODEX_BLOCK_START + "\n" + CODEX_BLOCK_END + "\n",
		"reversed":   CODEX_BLOCK_END + "\n" + CODEX_BLOCK_START + "\n",
	}
	for name, content := range cases {
		document, err := parseTOML([]byte(content))
		if err != nil {
			t.Fatalf("%s: parse: %v", name, err)
		}
		if _, _, _, err := document.blockMarkerPositions(); err == nil {
			t.Fatalf("%s: an unpaired block was accepted", name)
		}
	}
}

// TestTOMLScannerIgnoresTablesInsideStrings is why the scan is not a substring search:
// a '#' inside a string is part of the value, and an escaped quote does not close it.
func TestTOMLScannerIgnoresTablesInsideStrings(t *testing.T) {
	cases := map[string]struct {
		line string
		want string
	}{
		"comment":          {`model = "gpt-5" # my favourite`, `model = "gpt-5" `},
		"hash in string":   {`name = "a # b" # note`, `name = "a # b" `},
		"escaped quote":    {`name = "say \"hi\" # not a comment"`, `name = "say \"hi\" # not a comment"`},
		"literal string":   {`name = 'a # b' # note`, `name = 'a # b' `},
		"empty":            {``, ``},
		"hash in brackets": {`list = ["#", "[x]"] # note`, `list = ["#", "[x]"] `},
	}
	for name, testCase := range cases {
		got, err := scanTOMLCode(testCase.line)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got != testCase.want {
			t.Fatalf("%s: code = %q, want %q", name, got, testCase.want)
		}
	}
}

// TestTOMLScannerRefusesUnscannableLines covers the two shapes that would make the line
// model lie about the file.
func TestTOMLScannerRefusesUnscannableLines(t *testing.T) {
	for name, line := range map[string]string{
		"multi-line basic":   `instructions = """`,
		"multi-line literal": `instructions = '''`,
		"unterminated":       `name = "never closed`,
	} {
		if _, err := scanTOMLCode(line); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
}

// TestTOMLTopLevelKeyIsOnlyBeforeTheFirstTable is the plan's placement rule: a
// model_provider inside a table is a different key and must not be edited.
func TestTOMLTopLevelKeyIsOnlyBeforeTheFirstTable(t *testing.T) {
	content := "[profile.work]\nmodel_provider = \"openai\"\n"
	document, err := parseTOML([]byte(content))
	if err != nil {
		t.Fatal(err)
	}
	_, _, found, err := document.topLevelAssignment(CODEX_MODEL_PROVIDER)
	if err != nil {
		t.Fatal(err)
	}
	if found {
		t.Fatal("a key inside a table was read as the top-level one")
	}
	if err := document.setTopLevelKey(CODEX_MODEL_PROVIDER, CODEX_PROVIDER_ID); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(document.encode()), "model_provider = \"oh-my-cpa\"\n[profile.work]\n") {
		t.Fatalf("the new key is not above the first table:\n%s", document.encode())
	}
	if !strings.Contains(string(document.encode()), "[profile.work]\nmodel_provider = \"openai\"") {
		t.Fatal("the in-table key was changed")
	}
}

// TestTOMLHeaderNameNormalizesQuotedSegments covers a hand-written quoted header, which
// names the same table and therefore collides the same way.
func TestTOMLHeaderNameNormalizesQuotedSegments(t *testing.T) {
	document, err := parseTOML([]byte("[model_providers.\"oh-my-cpa\"]\nname = \"by hand\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, collides, err := document.unmanagedProviderTable(0, 0, false); err != nil || !collides {
		t.Fatal("a quoted copy of the provider table was not detected")
	}
}

// TestTOMLQuoteRoundTrips keeps the value an install writes readable by a TOML parser.
func TestTOMLQuoteRoundTrips(t *testing.T) {
	for _, value := range []string{"plain", `with "quotes"`, `back\slash`, "tab\there", "sk-cpa-abc"} {
		encoded := tomlQuote(value)
		decoded, err := parseTOMLString(encoded)
		if err != nil {
			t.Fatalf("%q: %v", value, err)
		}
		if decoded != value {
			t.Fatalf("%q round-tripped to %q", value, decoded)
		}
	}
}
