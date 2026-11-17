package keyclient

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

// jsonObject is a JSON object whose member order is preserved.
//
// A runtime configuration file belongs to the person who wrote it, not to this tool.
// Re-encoding it through a Go map would sort every key and rewrite the whole file, so
// the diff of an install would show churn the operator never made. This type keeps the
// original order, keeps every member it does not touch as its original bytes, and
// appends a new member at the end.
type jsonObject struct {
	keys   []string
	values map[string]json.RawMessage
}

// parseJSONObject reads a document that must be a single JSON object.
//
// Anything else — an array, a scalar, invalid JSON, trailing content, a duplicate
// member — is refused. A file this tool cannot understand is a file it must not
// rewrite: the common case is JSONC with comments, which would silently lose them.
func parseJSONObject(content []byte) (*jsonObject, error) {
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.UseNumber()
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '{' {
		return nil, errors.New("the document is not a JSON object")
	}
	object := &jsonObject{values: map[string]json.RawMessage{}}
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		key, ok := keyToken.(string)
		if !ok {
			return nil, errors.New("a member name is not a string")
		}
		if _, duplicate := object.values[key]; duplicate {
			return nil, errors.New("the object repeats the member " + key)
		}
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return nil, err
		}
		object.keys = append(object.keys, key)
		object.values[key] = raw
	}
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("the document has content after its top-level object")
	}
	return object, nil
}

// has reports whether the object has the member.
func (o *jsonObject) has(key string) bool {
	_, ok := o.values[key]
	return ok
}

// get returns the member's raw value.
func (o *jsonObject) get(key string) (json.RawMessage, bool) {
	raw, ok := o.values[key]
	return raw, ok
}

// setRaw stores an already-encoded value, keeping an existing member's position.
func (o *jsonObject) setRaw(key string, raw json.RawMessage) {
	if _, exists := o.values[key]; !exists {
		o.keys = append(o.keys, key)
	}
	o.values[key] = raw
}

// set encodes value with encoding/json and stores it.
func (o *jsonObject) set(key string, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	o.setRaw(key, encoded)
	return nil
}

// delete removes a member, reporting whether it was there.
func (o *jsonObject) delete(key string) bool {
	if _, exists := o.values[key]; !exists {
		return false
	}
	delete(o.values, key)
	for index, existing := range o.keys {
		if existing == key {
			o.keys = append(o.keys[:index], o.keys[index+1:]...)
			break
		}
	}
	return true
}

// stringValue reads a member that must be a JSON string. A member of another type is
// reported, because deciding what a number or an object means here would be a guess.
func (o *jsonObject) stringValue(key string) (string, bool, error) {
	raw, ok := o.values[key]
	if !ok {
		return "", false, nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", true, errors.New("the member " + key + " is not a string")
	}
	return value, true, nil
}

// child returns a nested member as an object. A missing member yields an empty object;
// a member of another type is an error rather than something to overwrite, because the
// operator may have written a value this tool does not understand.
func (o *jsonObject) child(key string) (*jsonObject, error) {
	raw, ok := o.values[key]
	if !ok || string(bytes.TrimSpace(raw)) == "null" {
		return &jsonObject{values: map[string]json.RawMessage{}}, nil
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, errors.New("the member " + key + " is not a JSON object")
	}
	return parseJSONObject(trimmed)
}

// setChild stores a nested object back under key.
func (o *jsonObject) setChild(key string, child *jsonObject) error {
	encoded, err := child.compact()
	if err != nil {
		return err
	}
	o.setRaw(key, encoded)
	return nil
}

// compact renders the object without indentation, for nesting inside another object.
func (o *jsonObject) compact() (json.RawMessage, error) {
	var buffer bytes.Buffer
	buffer.WriteByte('{')
	for index, key := range o.keys {
		if index > 0 {
			buffer.WriteByte(',')
		}
		encodedKey, err := json.Marshal(key)
		if err != nil {
			return nil, err
		}
		buffer.Write(encodedKey)
		buffer.WriteByte(':')
		buffer.Write(o.values[key])
	}
	buffer.WriteByte('}')
	return json.RawMessage(bytes.TrimRight(buffer.Bytes(), " ")), nil
}

// encodeIndent renders the object the way a person reads a settings file: two-space
// indentation, one member per line, original order kept.
func (o *jsonObject) encodeIndent() ([]byte, error) {
	if len(o.keys) == 0 {
		return []byte("{}"), nil
	}
	var buffer bytes.Buffer
	buffer.WriteString("{\n")
	for index, key := range o.keys {
		encodedKey, err := json.Marshal(key)
		if err != nil {
			return nil, err
		}
		buffer.WriteString("  ")
		buffer.Write(encodedKey)
		buffer.WriteString(": ")
		var indented bytes.Buffer
		if err := json.Indent(&indented, o.values[key], "  ", "  "); err != nil {
			return nil, err
		}
		buffer.Write(indented.Bytes())
		if index < len(o.keys)-1 {
			buffer.WriteByte(',')
		}
		buffer.WriteByte('\n')
	}
	buffer.WriteString("}\n")
	return buffer.Bytes(), nil
}

// sameJSON reports whether two encoded values mean the same thing. It is what the
// post-write check uses to prove a member this tool did not touch survived.
//
// The comparison is on compacted bytes rather than on decoded values: decoding into any
// would turn every number into a float64, and a value large enough to lose precision
// would read back as "changed" and roll back an install that was in fact correct.
func sameJSON(left, right json.RawMessage) bool {
	var leftBuffer, rightBuffer bytes.Buffer
	if err := json.Compact(&leftBuffer, left); err != nil {
		return false
	}
	if err := json.Compact(&rightBuffer, right); err != nil {
		return false
	}
	return bytes.Equal(leftBuffer.Bytes(), rightBuffer.Bytes())
}
