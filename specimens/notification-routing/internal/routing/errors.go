// Package routing owns channel, template, recipient, send fan-out, retry, and delivery-state policy.
package routing

import "errors"

var (
	// ErrConflict reports an optimistic-revision mismatch or a reused identity.
	ErrConflict = errors.New("routing conflict")
	// ErrDisabled reports an operation against a disabled channel.
	ErrDisabled = errors.New("channel is disabled")
	// ErrInvalid reports input that violates routing policy.
	ErrInvalid = errors.New("invalid routing input")
	// ErrNotFound reports an absent routing record.
	ErrNotFound = errors.New("routing record not found")
	// ErrPermanent marks a transport failure that no retry can repair.
	ErrPermanent = errors.New("permanent transport rejection")
)
