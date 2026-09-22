package ledger

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	// MaxMetadataBytes bounds the canonical encoding of one event's metadata.
	MaxMetadataBytes = 64 << 10
	// MaxMetadataDepth bounds nesting of objects and arrays inside metadata.
	MaxMetadataDepth = 32
	// MaxSafeInteger is the largest magnitude a canonical integer may carry.
	MaxSafeInteger = 1<<53 - 1
)

// CanonicalValue parses one JSON value and returns its canonical encoding.
//
// The canonical form is the wire-level contract shared with the independent
// verifier: objects carry their keys sorted by Unicode code point with no
// duplicates, there is no insignificant whitespace, strings are UTF-8 with only
// '"', '\', and U+0000-U+001F escaped (controls as lowercase \u00xx), numbers
// are integers within +/-(2^53-1) rendered without sign on zero, and null is a
// literal value rather than an omitted member. An absent value canonicalizes to
// null.
func CanonicalValue(raw json.RawMessage) (json.RawMessage, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return json.RawMessage("null"), nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("decode metadata: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("decode metadata: multiple JSON values")
	}
	var out bytes.Buffer
	if err := writeCanonical(&out, value, 0); err != nil {
		return nil, err
	}
	if out.Len() > MaxMetadataBytes {
		return nil, fmt.Errorf("canonical metadata exceeds %d bytes", MaxMetadataBytes)
	}
	return json.RawMessage(out.Bytes()), nil
}

func writeCanonical(out *bytes.Buffer, value any, depth int) error {
	if depth > MaxMetadataDepth {
		return fmt.Errorf("nesting exceeds %d levels", MaxMetadataDepth)
	}
	switch typed := value.(type) {
	case nil:
		out.WriteString("null")
		return nil
	case bool:
		out.WriteString(strconv.FormatBool(typed))
		return nil
	case string:
		return writeCanonicalString(out, typed)
	case json.Number:
		return writeCanonicalNumber(out, typed.String())
	case int64:
		return writeCanonicalNumber(out, strconv.FormatInt(typed, 10))
	case []any:
		return writeCanonicalArray(out, typed, depth)
	case map[string]any:
		return writeCanonicalObject(out, typed, depth)
	}
	return fmt.Errorf("unsupported JSON value %T", value)
}

func writeCanonicalNumber(out *bytes.Buffer, literal string) error {
	parsed, err := strconv.ParseInt(literal, 10, 64)
	if err != nil {
		return fmt.Errorf("number %q is not a decimal integer", literal)
	}
	if parsed > MaxSafeInteger || parsed < -MaxSafeInteger {
		return fmt.Errorf("integer %s exceeds the 2^53-1 safe range", literal)
	}
	out.WriteString(strconv.FormatInt(parsed, 10))
	return nil
}

func writeCanonicalString(out *bytes.Buffer, value string) error {
	if !utf8.ValidString(value) || strings.ContainsRune(value, utf8.RuneError) {
		return errors.New("string must be valid UTF-8 without U+FFFD")
	}
	out.WriteByte('"')
	for _, r := range value {
		switch {
		case r == '"':
			out.WriteString(`\"`)
		case r == '\\':
			out.WriteString(`\\`)
		case r < 0x20:
			fmt.Fprintf(out, `\u%04x`, r)
		default:
			out.WriteRune(r)
		}
	}
	out.WriteByte('"')
	return nil
}

func writeCanonicalArray(out *bytes.Buffer, values []any, depth int) error {
	out.WriteByte('[')
	for index, item := range values {
		if index > 0 {
			out.WriteByte(',')
		}
		if err := writeCanonical(out, item, depth+1); err != nil {
			return err
		}
	}
	out.WriteByte(']')
	return nil
}

func writeCanonicalObject(out *bytes.Buffer, object map[string]any, depth int) error {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out.WriteByte('{')
	for index, key := range keys {
		if index > 0 {
			out.WriteByte(',')
		}
		if err := writeCanonicalString(out, key); err != nil {
			return fmt.Errorf("object key: %w", err)
		}
		out.WriteByte(':')
		if err := writeCanonical(out, object[key], depth+1); err != nil {
			return err
		}
	}
	out.WriteByte('}')
	return nil
}

// CanonicalEntry renders the exact bytes hashed for one ledger position: the
// canonical object of every recorded member except the hash itself.
func CanonicalEntry(entry Entry) ([]byte, error) {
	metadata, err := CanonicalValue(entry.Metadata)
	if err != nil {
		return nil, fmt.Errorf("entry %s/%d metadata: %w", entry.Tenant, entry.Sequence, err)
	}
	var out bytes.Buffer
	members := []struct {
		key   string
		value any
	}{
		{"action", entry.Action},
		{"actor", entry.Actor},
		{"id", entry.ID},
		{"metadata", metadata},
		{"occurredAt", entry.OccurredAt},
		{"recordedAt", entry.RecordedAt},
		{"sequence", entry.Sequence},
		{"source", entry.Source},
		{"target", entry.Target},
		{"tenant", entry.Tenant},
	}
	sort.Slice(members, func(i, j int) bool { return members[i].key < members[j].key })
	out.WriteByte('{')
	for index, member := range members {
		if index > 0 {
			out.WriteByte(',')
		}
		if err := writeCanonicalString(&out, member.key); err != nil {
			return nil, err
		}
		out.WriteByte(':')
		if err := writeMember(&out, member.value); err != nil {
			return nil, fmt.Errorf("entry %s/%d %s: %w", entry.Tenant, entry.Sequence, member.key, err)
		}
	}
	out.WriteByte('}')
	return out.Bytes(), nil
}

func writeMember(out *bytes.Buffer, value any) error {
	if raw, ok := value.(json.RawMessage); ok {
		out.Write(raw)
		return nil
	}
	return writeCanonical(out, value, 0)
}
