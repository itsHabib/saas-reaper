package tunnel

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
	"time"
)

// Claim binds one subdomain to one agent credential. The credential itself is never stored;
// only its hash is, so the read plane cannot leak it and a database copy cannot replay it.
type Claim struct {
	Subdomain string
	TokenHash string
	Revision  int
	Revoked   bool
	CreatedAt time.Time
	RevokedAt time.Time
}

// State reports the claim's durable lifecycle state.
func (c Claim) State() ClaimState {
	if c.Revoked {
		return ClaimRevoked
	}
	return ClaimActive
}

const (
	tokenPrefix = "rtk_"
	tokenBytes  = 32
	// maxLabelLength is the DNS label ceiling; a subdomain is exactly one label.
	maxLabelLength = 63
)

// reservedSubdomains are labels a customer would expect to keep for the control plane or
// conventional web roles. They are rejected at claim time rather than shadowed at the edge.
var reservedSubdomains = map[string]bool{
	"www":     true,
	"control": true,
	"admin":   true,
	"api":     true,
}

// ValidateSubdomain accepts exactly one lowercase DNS label with no leading or trailing hyphen.
func ValidateSubdomain(subdomain string) error {
	if subdomain == "" || len(subdomain) > maxLabelLength {
		return fmt.Errorf("%w: subdomain must be 1 to %d characters", ErrInvalid, maxLabelLength)
	}
	if strings.HasPrefix(subdomain, "-") || strings.HasSuffix(subdomain, "-") {
		return fmt.Errorf("%w: subdomain must not start or end with a hyphen", ErrInvalid)
	}
	for _, r := range subdomain {
		if !labelRune(r) {
			return fmt.Errorf("%w: subdomain may contain only lowercase letters, digits, and hyphens", ErrInvalid)
		}
	}
	if reservedSubdomains[subdomain] {
		return fmt.Errorf("%w: subdomain %q is reserved", ErrInvalid, subdomain)
	}
	return nil
}

func labelRune(r rune) bool {
	if r >= 'a' && r <= 'z' {
		return true
	}
	if r >= '0' && r <= '9' {
		return true
	}
	return r == '-'
}

// NewToken draws a fresh agent credential from the supplied randomness. The value is shown to
// the operator exactly once; callers persist only HashToken of it.
func NewToken(random io.Reader) (string, error) {
	if random == nil {
		random = rand.Reader
	}
	raw := make([]byte, tokenBytes)
	if _, err := io.ReadFull(random, raw); err != nil {
		return "", fmt.Errorf("draw agent token: %w", err)
	}
	return tokenPrefix + base64.RawURLEncoding.EncodeToString(raw), nil
}

// HashToken derives the stored lookup key for a credential.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// ValidateToken rejects credentials that cannot have been issued by NewToken before any lookup.
func ValidateToken(token string) error {
	if !strings.HasPrefix(token, tokenPrefix) {
		return fmt.Errorf("%w: malformed agent token", ErrUnauthorized)
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(token, tokenPrefix))
	if err != nil || len(raw) != tokenBytes {
		return fmt.Errorf("%w: malformed agent token", ErrUnauthorized)
	}
	return nil
}
