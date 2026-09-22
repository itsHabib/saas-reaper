// Package ledger owns audit-event validation, canonical encoding, hash chaining,
// idempotent append, and chain verification policy.
package ledger

import "errors"

var (
	// ErrInvalid reports an event that violates ledger policy.
	ErrInvalid = errors.New("invalid audit event")
	// ErrConflict reports a replayed event ID whose content differs from the recorded entry.
	ErrConflict = errors.New("audit event conflict")
	// ErrBroken reports a chain whose recomputed hashes or sequences disagree with the export.
	ErrBroken = errors.New("audit chain is broken")
)
