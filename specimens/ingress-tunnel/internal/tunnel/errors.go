// Package tunnel owns ingress-tunnel policy: which subdomain a credential may claim, how a live
// agent link is attached and superseded, the claim lifecycle table, host-to-subdomain
// resolution, and the reconnect schedule. It consumes narrow interfaces and never imports a
// transport or persistence mechanism.
package tunnel

import "errors"

// Policy error classes. Transports translate them into their own status codes.
var (
	// ErrInvalid marks a request the policy refuses on its shape.
	ErrInvalid = errors.New("invalid")
	// ErrConflict marks a stale revision or an already-taken subdomain.
	ErrConflict = errors.New("conflict")
	// ErrNotFound marks a subdomain with no claim.
	ErrNotFound = errors.New("not found")
	// ErrUnauthorized marks an agent credential that maps to no usable claim.
	ErrUnauthorized = errors.New("unauthorized")
	// ErrRevoked marks a claim whose credential has been withdrawn.
	ErrRevoked = errors.New("revoked")
)
