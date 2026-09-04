package routing

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	// MaxTemplateBytes bounds one template body.
	MaxTemplateBytes = 64 << 10
	// MaxPayloadBytes bounds the JSON payload rendered into a template.
	MaxPayloadBytes = 256 << 10
	maxSubjectBytes = 512
)

var (
	placeholder  = regexp.MustCompile(`\{\{\s*([^{}]*?)\s*\}\}`)
	variablePath = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)*$`)
)

// Template is one channel-specific variant of a named notification.
type Template struct {
	Key       string
	ChannelID string
	Subject   string
	Body      string
	Variables []string
	CreatedAt time.Time
}

// Rendered is the exact text queued for one delivery.
type Rendered struct {
	Subject string
	Body    string
}

// Payload is a decoded JSON object whose numbers keep their literal digits.
type Payload map[string]any

// NewTemplate validates placeholder syntax and records the variables a send must supply.
func NewTemplate(key, channelID, subject, body string, now time.Time) (Template, error) {
	if err := validateOwnedID("template", key); err != nil {
		return Template{}, err
	}
	if err := validateOwnedID("channel", channelID); err != nil {
		return Template{}, err
	}
	if len(subject) > maxSubjectBytes || strings.ContainsAny(subject, "\r\n") {
		return Template{}, fmt.Errorf("%w: subject must be a single line of at most %d bytes", ErrInvalid, maxSubjectBytes)
	}
	if body == "" || len(body) > MaxTemplateBytes {
		return Template{}, fmt.Errorf("%w: body must contain 1-%d bytes", ErrInvalid, MaxTemplateBytes)
	}
	variables, err := placeholders(subject + "\x00" + body)
	if err != nil {
		return Template{}, err
	}
	return Template{
		Key:       key,
		ChannelID: channelID,
		Subject:   subject,
		Body:      body,
		Variables: variables,
		CreatedAt: now.UTC(),
	}, nil
}

func placeholders(text string) ([]string, error) {
	seen := map[string]bool{}
	for _, match := range placeholder.FindAllStringSubmatch(text, -1) {
		if !variablePath.MatchString(match[1]) {
			return nil, fmt.Errorf("%w: placeholder %q must be a dotted variable path", ErrInvalid, match[0])
		}
		seen[match[1]] = true
	}
	remainder := placeholder.ReplaceAllString(text, "")
	if strings.Contains(remainder, "{{") || strings.Contains(remainder, "}}") {
		return nil, fmt.Errorf("%w: unbalanced placeholder braces", ErrInvalid)
	}
	variables := make([]string, 0, len(seen))
	for name := range seen {
		variables = append(variables, name)
	}
	sort.Strings(variables)
	return variables, nil
}

// ParsePayload decodes exactly one JSON object without losing numeric digits.
func ParsePayload(raw []byte) (Payload, error) {
	if len(raw) == 0 || len(raw) > MaxPayloadBytes {
		return nil, fmt.Errorf("%w: payload must contain 1-%d bytes", ErrInvalid, MaxPayloadBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var payload Payload
	if err := decoder.Decode(&payload); err != nil {
		return nil, fmt.Errorf("%w: payload must be one JSON object: %w", ErrInvalid, err)
	}
	if decoder.More() {
		return nil, fmt.Errorf("%w: payload must be one JSON object", ErrInvalid)
	}
	if payload == nil {
		return nil, fmt.Errorf("%w: payload must be a JSON object, not null", ErrInvalid)
	}
	return payload, nil
}

// Render substitutes every placeholder or rejects the send before anything is queued.
func (t Template) Render(payload Payload, kind ChannelKind) (Rendered, error) {
	subject, err := t.substitute(t.Subject, payload, kind, true)
	if err != nil {
		return Rendered{}, err
	}
	body, err := t.substitute(t.Body, payload, kind, false)
	if err != nil {
		return Rendered{}, err
	}
	return Rendered{Subject: subject, Body: body}, nil
}

func (t Template) substitute(text string, payload Payload, kind ChannelKind, singleLine bool) (string, error) {
	var failure error
	rendered := placeholder.ReplaceAllStringFunc(text, func(match string) string {
		if failure != nil {
			return ""
		}
		path := placeholder.FindStringSubmatch(match)[1]
		value, err := t.lookup(payload, path)
		if err != nil {
			failure = err
			return ""
		}
		if singleLine && strings.ContainsAny(value, "\r\n") {
			failure = fmt.Errorf("%w: template %s variable %q cannot contain line breaks in a subject", ErrInvalid, t.Key, path)
			return ""
		}
		return escapeValue(kind, value)
	})
	if failure != nil {
		return "", failure
	}
	return rendered, nil
}

func (t Template) lookup(payload Payload, path string) (string, error) {
	var current any = map[string]any(payload)
	for _, segment := range strings.Split(path, ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return "", fmt.Errorf("%w: template %s variable %q is missing from payload", ErrInvalid, t.Key, path)
		}
		next, present := object[segment]
		if !present {
			return "", fmt.Errorf("%w: template %s variable %q is missing from payload", ErrInvalid, t.Key, path)
		}
		current = next
	}
	return scalarText(t.Key, path, current)
}

func scalarText(key, path string, value any) (string, error) {
	switch typed := value.(type) {
	case string:
		return typed, nil
	case json.Number:
		return typed.String(), nil
	case bool:
		if typed {
			return "true", nil
		}
		return "false", nil
	default:
		return "", fmt.Errorf("%w: template %s variable %q must be a string, number, or boolean", ErrInvalid, key, path)
	}
}

func escapeValue(kind ChannelKind, value string) string {
	if kind != KindSlackWebhook {
		return value
	}
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(value)
}
