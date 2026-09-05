package tunnel

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestValidateSubdomainAcceptsOneLowercaseLabel(t *testing.T) {
	for _, subdomain := range []string{"a", "acme", "acme-billing", "a1", "1a", strings.Repeat("x", 63)} {
		if err := ValidateSubdomain(subdomain); err != nil {
			t.Fatalf("%q rejected: %v", subdomain, err)
		}
	}
}

func TestValidateSubdomainRejectsEverythingElse(t *testing.T) {
	cases := map[string]string{
		"empty":         "",
		"too long":      strings.Repeat("x", 64),
		"uppercase":     "Acme",
		"leading dash":  "-acme",
		"trailing dash": "acme-",
		"dot":           "acme.dev",
		"underscore":    "acme_dev",
		"space":         "acme dev",
		"unicode":       "acmé",
		"reserved www":  "www",
		"reserved api":  "api",
	}
	for name, subdomain := range cases {
		err := ValidateSubdomain(subdomain)
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s: %q accepted (err=%v)", name, subdomain, err)
		}
	}
}

func TestNewTokenIsPrefixedAndHashesStably(t *testing.T) {
	token, err := NewToken(bytes.NewReader(bytes.Repeat([]byte{7}, 64)))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(token, "rtk_") {
		t.Fatalf("token %q lacks prefix", token)
	}
	if err := ValidateToken(token); err != nil {
		t.Fatalf("freshly issued token rejected: %v", err)
	}
	if len(HashToken(token)) != 64 {
		t.Fatal("hash is not a 64-hex digest")
	}
	other, err := NewToken(bytes.NewReader(bytes.Repeat([]byte{8}, 64)))
	if err != nil {
		t.Fatal(err)
	}
	if HashToken(other) == HashToken(token) {
		t.Fatal("distinct tokens hashed equal")
	}
}

func TestNewTokenFailsOnShortRandomness(t *testing.T) {
	if _, err := NewToken(bytes.NewReader([]byte{1, 2, 3})); err == nil {
		t.Fatal("short randomness produced a token")
	}
}

func TestValidateTokenRejectsMalformedCredentials(t *testing.T) {
	for _, token := range []string{"", "rtk_", "rtk_short", "nope_" + strings.Repeat("A", 43), "rtk_" + strings.Repeat("!", 43)} {
		if err := ValidateToken(token); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("%q accepted (err=%v)", token, err)
		}
	}
}
