// Package incident owns pager policy: services, escalation, lifecycle, dedup, and notification planning.
package incident

import "errors"

var (
	// ErrConflict reports an optimistic-revision or uniqueness race that the caller may retry.
	ErrConflict = errors.New("incident revision conflict")
	// ErrInvalid reports input that violates incident policy.
	ErrInvalid = errors.New("invalid incident input")
	// ErrNotFound reports an absent record.
	ErrNotFound = errors.New("incident record not found")
	// ErrPermanent marks a notification failure that no retry can repair.
	ErrPermanent = errors.New("permanent notification failure")
)
