package routing

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestNewTemplateRecordsSortedVariables(t *testing.T) {
	template, err := NewTemplate(
		"invoice-paid",
		"email",
		"Invoice {{ invoice.id }} paid",
		"Hello {{customer.name}}, {{invoice.amount}} {{ invoice.currency }} received.",
		time.Unix(1, 0),
	)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"customer.name", "invoice.amount", "invoice.currency", "invoice.id"}
	if strings.Join(template.Variables, ",") != strings.Join(want, ",") {
		t.Fatalf("variables = %v, want %v", template.Variables, want)
	}
}

func TestNewTemplateRejectsUnsafeInputs(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		subject string
		body    string
	}{
		{name: "unsafe key", key: "Invoice.Paid", body: "ok"},
		{name: "empty body", key: "invoice-paid", body: ""},
		{name: "multi-line subject", key: "invoice-paid", subject: "a\nb", body: "ok"},
		{name: "unbalanced braces", key: "invoice-paid", body: "{{ open"},
		{name: "invalid path", key: "invoice-paid", body: "{{ 1bad }}"},
		{name: "nested braces", key: "invoice-paid", body: "{{ a }} }}"},
		{name: "oversized body", key: "invoice-paid", body: strings.Repeat("x", MaxTemplateBytes+1)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewTemplate(test.key, "email", test.subject, test.body, time.Unix(1, 0))
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("error = %v, want invalid", err)
			}
		})
	}
}

func TestRenderSubstitutesScalarsAndEscapesForSlack(t *testing.T) {
	template, err := NewTemplate(
		"invoice-paid",
		"chat",
		"",
		"{{customer}} paid {{amount}} ({{paid}}) <{{link}}>",
		time.Unix(1, 0),
	)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := ParsePayload([]byte(`{"customer":"A&B <co>","amount":12.50,"paid":true,"link":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := template.Render(payload, KindSlackWebhook)
	if err != nil {
		t.Fatal(err)
	}
	if rendered.Body != "A&amp;B &lt;co&gt; paid 12.50 (true) <x>" {
		t.Fatalf("slack body = %q", rendered.Body)
	}
	plain, err := template.Render(payload, KindSMTP)
	if err != nil {
		t.Fatal(err)
	}
	if plain.Body != "A&B <co> paid 12.50 (true) <x>" {
		t.Fatalf("email body = %q", plain.Body)
	}
}

func TestRenderRejectsMissingAndUnrenderableVariables(t *testing.T) {
	tests := []struct {
		name    string
		subject string
		body    string
		payload string
		want    string
	}{
		{name: "missing", body: "{{ amount }}", payload: `{"other":1}`, want: `"amount" is missing`},
		{name: "missing nested", body: "{{ invoice.id }}", payload: `{"invoice":{}}`, want: `"invoice.id" is missing`},
		{name: "scalar parent", body: "{{ invoice.id }}", payload: `{"invoice":"flat"}`, want: `"invoice.id" is missing`},
		{name: "object value", body: "{{ invoice }}", payload: `{"invoice":{}}`, want: "string, number, or boolean"},
		{name: "null value", body: "{{ invoice }}", payload: `{"invoice":null}`, want: "string, number, or boolean"},
		{name: "subject line break", subject: "{{ title }}", body: "x", payload: `{"title":"a\nb"}`, want: "line breaks"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			template, err := NewTemplate("invoice-paid", "email", test.subject, test.body, time.Unix(1, 0))
			if err != nil {
				t.Fatal(err)
			}
			payload, err := ParsePayload([]byte(test.payload))
			if err != nil {
				t.Fatal(err)
			}
			_, err = template.Render(payload, KindSMTP)
			if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want invalid containing %q", err, test.want)
			}
		})
	}
}

func TestParsePayloadRequiresOneObject(t *testing.T) {
	for _, raw := range []string{"", "null", "[1]", `{"a":1} {"b":2}`, `"text"`, "{"} {
		if _, err := ParsePayload([]byte(raw)); !errors.Is(err, ErrInvalid) {
			t.Fatalf("payload %q error = %v, want invalid", raw, err)
		}
	}
}
