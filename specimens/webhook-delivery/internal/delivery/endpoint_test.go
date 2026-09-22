package delivery

import (
	"strings"
	"testing"
	"time"
)

const testSecret = "whsec_MDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDA="

func TestNewEndpointAcceptsStandardSecret(t *testing.T) {
	endpoint, err := NewEndpoint("billing", "http://127.0.0.1:19001/webhook", testSecret, time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	if !endpoint.Enabled || endpoint.Revision != 1 {
		t.Fatalf("endpoint = %#v", endpoint)
	}
}

func TestNewEndpointRejectsUnsafeInputs(t *testing.T) {
	tests := []struct {
		name   string
		id     string
		url    string
		secret string
	}{
		{name: "unsafe id", id: "bad.id", url: "https://example.com/hook", secret: testSecret},
		{name: "credential URL", id: "safe", url: "https://user@example.com/hook", secret: testSecret},
		{name: "wrong scheme", id: "safe", url: "file:///tmp/hook", secret: testSecret},
		{name: "short secret", id: "safe", url: "https://example.com/hook", secret: "whsec_YQ=="},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewEndpoint(test.id, test.url, test.secret, time.Now())
			if err == nil || !strings.Contains(err.Error(), ErrInvalid.Error()) {
				t.Fatalf("error = %v, want invalid input", err)
			}
		})
	}
}
