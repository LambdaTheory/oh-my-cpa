package keyclient

import (
	"errors"
	"fmt"
	"strings"
)

const (
	// CODEX_BLOCK_START and CODEX_BLOCK_END bracket the one region of a Codex
	// configuration this tool owns. They carry the removal command so a person who
	// opens the file months later can undo this without reading any documentation.
	CODEX_BLOCK_START = "# >>> oh-my-cpa key (managed; `oh-my-cpa key purge --repo <repo>` removes this) >>>"
	CODEX_BLOCK_END   = "# <<< oh-my-cpa key <<<"

	// CODEX_PROVIDER_ID is the provider name in both the managed table and the
	// top-level model_provider line.
	CODEX_PROVIDER_ID = "oh-my-cpa"

	// CODEX_WIRE_API is the only API shape a Codex provider accepts for a gateway that
	// speaks the OpenAI Responses protocol.
	CODEX_WIRE_API = "responses"
)

// tomlDocument is a configuration file held as its original lines, so a managed block
// can be inserted or removed without re-encoding — and therefore without reordering,
// reformatting or dropping a comment in — the parts this tool does not own.
//
// It is deliberately not a TOML parser. Decoding and re-encoding would discard
// comments and key order, which is a rewrite of someone else's file; the plan chose a
// self-contained managed block for exactly that reason. What this type must get right
// is narrow: where the top-level keys end, where the managed block starts and stops,
// and whether a table the editor would collide with already exists.
type tomlDocument struct {
	lines        []string
	finalNewline bool
}

// parseTOML reads a configuration as lines and refuses the shapes a line-level editor
// cannot follow safely: multi-line strings, an unterminated string, and anything it
// cannot scan. Refusing is the documented behaviour; guessing at a boundary would
// splice a managed block into the middle of someone's value.
func parseTOML(content []byte) (*tomlDocument, error) {
	document := &tomlDocument{}
	text := string(content)
	if text == "" {
		document.finalNewline = true
		return document, nil
	}
	document.finalNewline = strings.HasSuffix(text, "\n")
	if document.finalNewline {
		text = strings.TrimSuffix(text, "\n")
	}
	document.lines = strings.Split(text, "\n")
	if _, err := document.layout(); err != nil {
		return nil, err
	}
	return document, nil
}

// tomlLine is what one line contributes to the scan: its code with the comment
// removed, whether it opens a table, and which table that is.
type tomlLine struct {
	code      string
	topLevel  bool
	isHeader  bool
	tableName string
}

// layout scans every line once. Callers ask for positions rather than rescanning, so
// two edits cannot disagree about which line is the first table header.
func (d *tomlDocument) layout() ([]tomlLine, error) {
	scanned := make([]tomlLine, len(d.lines))
	seenHeader := false
	for index, line := range d.lines {
		code, err := scanTOMLCode(line)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", index+1, err)
		}
		entry := tomlLine{code: code}
		trimmed := strings.TrimSpace(code)
		if strings.HasPrefix(trimmed, "[[") {
			entry.isHeader = true
			name, ok := tomlHeaderName(trimmed, "[[", "]]")
			if !ok {
				return nil, fmt.Errorf("line %d: cannot read an array-of-tables header", index+1)
			}
			entry.tableName = name
		} else if strings.HasPrefix(trimmed, "[") {
			entry.isHeader = true
			name, ok := tomlHeaderName(trimmed, "[", "]")
			if !ok {
				return nil, fmt.Errorf("line %d: cannot read a table header", index+1)
			}
			entry.tableName = name
		} else {
			entry.topLevel = !seenHeader
		}
		if entry.isHeader {
			seenHeader = true
		}
		scanned[index] = entry
	}
	return scanned, nil
}

// scanTOMLCode returns the part of a line before an unquoted '#'. It refuses a
// multi-line string delimiter and an unterminated string, the two cases where the
// line-level model stops describing the file.
func scanTOMLCode(line string) (string, error) {
	var builder strings.Builder
	inBasic, inLiteral := false, false
	for index := 0; index < len(line); index++ {
		character := line[index]
		switch {
		case inBasic:
			builder.WriteByte(character)
			if character == '\\' && index+1 < len(line) {
				index++
				builder.WriteByte(line[index])
				continue
			}
			if character == '"' {
				inBasic = false
			}
		case inLiteral:
			builder.WriteByte(character)
			if character == '\'' {
				inLiteral = false
			}
		case character == '"':
			if strings.HasPrefix(line[index:], `"""`) {
				return "", errors.New("multi-line strings are not edited line by line")
			}
			inBasic = true
			builder.WriteByte(character)
		case character == '\'':
			if strings.HasPrefix(line[index:], "'''") {
				return "", errors.New("multi-line strings are not edited line by line")
			}
			inLiteral = true
			builder.WriteByte(character)
		case character == '#':
			return builder.String(), nil
		default:
			builder.WriteByte(character)
		}
	}
	if inBasic || inLiteral {
		return "", errors.New("a string does not close on its line")
	}
	return builder.String(), nil
}

// tomlHeaderName reads the table name out of a header line and normalizes a quoted
// path segment, so `[model_providers."oh-my-cpa"]` and `[model_providers.oh-my-cpa]`
// are recognised as the same table.
func tomlHeaderName(trimmed, open, close string) (string, bool) {
	if !strings.HasSuffix(trimmed, close) {
		return "", false
	}
	inner := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(trimmed, open), close))
	if inner == "" {
		return "", false
	}
	segments := strings.Split(inner, ".")
	for index, segment := range segments {
		segment = strings.TrimSpace(segment)
		if len(segment) >= 2 {
			if (segment[0] == '"' && segment[len(segment)-1] == '"') || (segment[0] == '\'' && segment[len(segment)-1] == '\'') {
				segment = segment[1 : len(segment)-1]
			}
		}
		segments[index] = segment
	}
	return strings.Join(segments, "."), true
}

// blockMarkerPositions locates the managed block. A start without an end, or an end
// without a start, means a previous write was interrupted or hand-edited, and the tool
// stops rather than guess where the region ends.
func (d *tomlDocument) blockMarkerPositions() (start, end int, found bool, err error) {
	start, end = -1, -1
	for index, line := range d.lines {
		switch strings.TrimSpace(line) {
		case CODEX_BLOCK_START:
			if start >= 0 {
				return 0, 0, false, errors.New("the managed block starts twice")
			}
			start = index
		case CODEX_BLOCK_END:
			if start < 0 {
				return 0, 0, false, errors.New("the managed block ends without starting")
			}
			if end >= 0 {
				return 0, 0, false, errors.New("the managed block ends twice")
			}
			end = index
		}
	}
	switch {
	case start < 0 && end < 0:
		return 0, 0, false, nil
	case start >= 0 && end > start:
		return start, end, true, nil
	default:
		return 0, 0, false, errors.New("the managed block starts without ending")
	}
}

// blockValues reads the values inside an existing managed block.
func (d *tomlDocument) blockValues(start, end int) map[string]string {
	values := map[string]string{}
	for index := start + 1; index < end; index++ {
		code, err := scanTOMLCode(d.lines[index])
		if err != nil {
			continue
		}
		key, value, ok := splitTOMLAssignment(code)
		if !ok {
			continue
		}
		if parsed, parseErr := parseTOMLString(value); parseErr == nil {
			values[key] = parsed
		}
	}
	return values
}

// unmanagedProviderTable reports the line of a [model_providers.oh-my-cpa] table that
// is not inside the managed block. Writing a second, equally named table makes a file
// TOML rejects, so the tool stops and lets the operator decide which one to keep.
func (d *tomlDocument) unmanagedProviderTable(blockStart, blockEnd int, hasBlock bool) (int, bool, error) {
	scanned, err := d.layout()
	if err != nil {
		return 0, false, err
	}
	for index, line := range scanned {
		if hasBlock && index >= blockStart && index <= blockEnd {
			continue
		}
		if line.isHeader && line.tableName == "model_providers."+CODEX_PROVIDER_ID {
			return index, true, nil
		}
	}
	return 0, false, nil
}

// topLevelAssignment finds a top-level key. The plan requires model_provider to sit
// before the first table header, so a same-named key inside a table is not this key
// and is left alone.
func (d *tomlDocument) topLevelAssignment(name string) (index int, value string, ok bool, err error) {
	scanned, err := d.layout()
	if err != nil {
		return 0, "", false, err
	}
	found := -1
	for position, line := range scanned {
		if !line.topLevel {
			continue
		}
		key, raw, isAssignment := splitTOMLAssignment(line.code)
		if !isAssignment || key != name {
			continue
		}
		if found >= 0 {
			return 0, "", false, fmt.Errorf("the top-level key %s appears twice", name)
		}
		parsed, parseErr := parseTOMLString(raw)
		if parseErr != nil {
			return 0, "", false, fmt.Errorf("the value of %s is not a string", name)
		}
		found, value = position, parsed
	}
	if found < 0 {
		return 0, "", false, nil
	}
	return found, value, true, nil
}

// headerInsertionPoint is the index of the first table header, or len(lines) when the
// file has none: that is where a top-level key has to go to stay top-level.
func (d *tomlDocument) headerInsertionPoint() (int, error) {
	scanned, err := d.layout()
	if err != nil {
		return 0, err
	}
	for index, line := range scanned {
		if line.isHeader {
			return index, nil
		}
	}
	return len(d.lines), nil
}

// removeBlock deletes the managed block and the single blank line that separated it
// from the content above, so an install followed by a purge leaves the surrounding
// text as it was.
func (d *tomlDocument) removeBlock(start, end int) {
	from := start
	if from > 0 && strings.TrimSpace(d.lines[from-1]) == "" {
		from--
	}
	d.lines = append(d.lines[:from:from], d.lines[end+1:]...)
}

// appendBlock puts the managed block at the end, separated from prior content by
// exactly one blank line. Appending at a fixed place is what makes a repeated install
// byte-identical instead of accumulating blank lines.
func (d *tomlDocument) appendBlock(block []string) {
	d.trimTrailingBlankLines()
	if len(d.lines) > 0 {
		d.lines = append(d.lines, "")
	}
	d.lines = append(d.lines, block...)
	d.finalNewline = true
}

// trimTrailingBlankLines removes blank lines at the end of the file.
func (d *tomlDocument) trimTrailingBlankLines() {
	for len(d.lines) > 0 && strings.TrimSpace(d.lines[len(d.lines)-1]) == "" {
		d.lines = d.lines[:len(d.lines)-1]
	}
}

// setTopLevelKey writes a top-level string value.
//
// A key that is already there keeps its line, so a reinstall rewrites one line and
// nothing else. A new one goes after the last top-level line, before the blank line
// that separates the top-level keys from the first table: that is the only place it
// stays top-level, and it is the shape a purge can undo exactly.
func (d *tomlDocument) setTopLevelKey(name, value string) error {
	index, _, found, err := d.topLevelAssignment(name)
	if err != nil {
		return err
	}
	assignment := name + " = " + tomlQuote(value)
	if found {
		d.lines[index] = assignment
		return nil
	}
	position, err := d.headerInsertionPoint()
	if err != nil {
		return err
	}
	// The insertion point is the first table header; step back over the blank lines
	// above it, so the key lands beside the other top-level keys and the blank line
	// stays where it was.
	for position > 0 && strings.TrimSpace(d.lines[position-1]) == "" {
		position--
	}
	inserted := make([]string, 0, len(d.lines)+1)
	inserted = append(inserted, d.lines[:position]...)
	inserted = append(inserted, assignment)
	inserted = append(inserted, d.lines[position:]...)
	d.lines = inserted
	return nil
}

// deleteTopLevelKey removes a top-level key. It also drops one blank line when the
// removal leaves two in a row, which is exactly what setTopLevelKey added, so a purge
// after an install restores the original spacing.
func (d *tomlDocument) deleteTopLevelKey(name string) error {
	index, _, found, err := d.topLevelAssignment(name)
	if err != nil || !found {
		return err
	}
	d.lines = append(d.lines[:index:index], d.lines[index+1:]...)
	if index > 0 && index < len(d.lines) && strings.TrimSpace(d.lines[index-1]) == "" && strings.TrimSpace(d.lines[index]) == "" {
		d.lines = append(d.lines[:index:index], d.lines[index+1:]...)
	}
	d.trimTrailingBlankLines()
	return nil
}

// encode renders the file with a trailing newline.
func (d *tomlDocument) encode() []byte {
	if len(d.lines) == 0 {
		return nil
	}
	text := strings.Join(d.lines, "\n")
	if d.finalNewline {
		text += "\n"
	}
	return []byte(text)
}

// splitTOMLAssignment splits `key = value` at its first unquoted '='. A key may be
// bare or quoted; both are reduced to their text so a caller can compare them.
func splitTOMLAssignment(code string) (key, value string, ok bool) {
	trimmed := strings.TrimSpace(code)
	inBasic, inLiteral := false, false
	for index := 0; index < len(trimmed); index++ {
		character := trimmed[index]
		switch {
		case inBasic:
			if character == '\\' && index+1 < len(trimmed) {
				index++
				continue
			}
			if character == '"' {
				inBasic = false
			}
		case inLiteral:
			if character == '\'' {
				inLiteral = false
			}
		case character == '"':
			inBasic = true
		case character == '\'':
			inLiteral = true
		case character == '=':
			key = strings.TrimSpace(trimmed[:index])
			value = strings.TrimSpace(trimmed[index+1:])
			if len(key) >= 2 && ((key[0] == '"' && key[len(key)-1] == '"') || (key[0] == '\'' && key[len(key)-1] == '\'')) {
				key = key[1 : len(key)-1]
			}
			return key, value, key != "" && value != ""
		}
	}
	return "", "", false
}

// parseTOMLString reads a single-line TOML string value: a basic string with escapes
// or a literal string. A value that is not exactly one string is refused, because the
// editor has no way to preserve a type it does not model.
func parseTOMLString(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if len(trimmed) < 2 {
		return "", errors.New("not a TOML string")
	}
	quote := trimmed[0]
	if quote != '"' && quote != '\'' {
		return "", errors.New("not a TOML string")
	}
	if trimmed[len(trimmed)-1] != quote {
		return "", errors.New("not a TOML string")
	}
	inner := trimmed[1 : len(trimmed)-1]
	if quote == '\'' {
		if strings.Contains(inner, "'") {
			return "", errors.New("not a TOML string")
		}
		return inner, nil
	}
	var builder strings.Builder
	for index := 0; index < len(inner); index++ {
		character := inner[index]
		if character == '"' {
			return "", errors.New("not a TOML string")
		}
		if character != '\\' {
			builder.WriteByte(character)
			continue
		}
		if index+1 >= len(inner) {
			return "", errors.New("not a TOML string")
		}
		index++
		switch inner[index] {
		case '"', '\\':
			builder.WriteByte(inner[index])
		case 'n':
			builder.WriteByte('\n')
		case 't':
			builder.WriteByte('\t')
		default:
			return "", errors.New("not a TOML string")
		}
	}
	return builder.String(), nil
}

// tomlQuote renders a basic string, escaping the characters that would otherwise end
// the string or start an escape.
func tomlQuote(value string) string {
	var builder strings.Builder
	builder.WriteByte('"')
	for _, character := range value {
		switch {
		case character == '"':
			builder.WriteString(`\"`)
		case character == '\\':
			builder.WriteString(`\\`)
		case character == '\n':
			builder.WriteString(`\n`)
		case character == '\t':
			builder.WriteString(`\t`)
		case character < 0x20:
			fmt.Fprintf(&builder, `\u%04X`, character)
		default:
			builder.WriteRune(character)
		}
	}
	builder.WriteByte('"')
	return builder.String()
}

// codexManagedBlock renders the managed region for one repository.
func codexManagedBlock(record RepoState, baseURL, key string) []string {
	return []string{
		CODEX_BLOCK_START,
		"[model_providers." + CODEX_PROVIDER_ID + "]",
		"name = " + tomlQuote("Oh My CPA ("+record.Repo+")"),
		"base_url = " + tomlQuote(strings.TrimSuffix(baseURL, "/")+"/v1"),
		"wire_api = " + tomlQuote(CODEX_WIRE_API),
		"experimental_bearer_token = " + tomlQuote(key),
		CODEX_BLOCK_END,
	}
}
