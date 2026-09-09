// Package delivery owns endpoint, publication, signing, retry, and delivery-state policy.
package delivery

import "errors"

var (
	// ErrConflict reports an optimistic-revision mismatch.
	ErrConflict = errors.New("revision conflict")
	// ErrDisabled reports a delivery operation against a disabled endpoint.
	ErrDisabled = errors.New("endpoint is disabled")
	// ErrInvalid reports input that violates delivery policy.
	ErrInvalid = errors.New("invalid delivery input")
	// ErrNotFound reports an absent delivery record.
	ErrNotFound = errors.New("delivery record not found")
	// errPermanent marks an attempt failure that no retry can repair.
	errPermanent = errors.New("permanent delivery failure")
)
