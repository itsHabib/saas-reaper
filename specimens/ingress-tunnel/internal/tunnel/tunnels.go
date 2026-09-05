package tunnel

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"
)

// Store is the durable authority policy consumes. Every write that changes a claim carries
// the audit rows that record it, and the store commits them in one transaction.
type Store interface {
	InsertClaim(context.Context, Claim, AuditEntry) error
	ClaimByTokenHash(context.Context, string) (Claim, error)
	ClaimBySubdomain(context.Context, string) (Claim, error)
	RevokeClaim(context.Context, string, int, time.Time, []AuditEntry) (Claim, error)
	AppendAudit(context.Context, []AuditEntry) error
	ListClaims(context.Context) ([]Claim, error)
}

// Service composes the store and the routing table under the lifecycle table.
type Service struct {
	store    Store
	registry *Registry
	actor    string
	now      func() time.Time
	random   io.Reader
	logger   *slog.Logger
}

// NewService validates the composition. actor is the authenticated management principal.
func NewService(store Store, registry *Registry, actor string, now func() time.Time, random io.Reader, logger *slog.Logger) (*Service, error) {
	if store == nil || registry == nil || now == nil || logger == nil {
		return nil, errors.New("store, registry, clock, and logger are required")
	}
	if strings.TrimSpace(actor) == "" {
		return nil, errors.New("management actor is required")
	}
	return &Service{store: store, registry: registry, actor: actor, now: now, random: random, logger: logger}, nil
}

// Issued is a fresh claim with the one-time credential that proves it.
type Issued struct {
	Claim Claim
	Token string
}

// Claim reserves subdomain for a new credential.
func (s *Service) Claim(ctx context.Context, subdomain string) (Issued, error) {
	if err := ValidateSubdomain(subdomain); err != nil {
		return Issued{}, err
	}
	token, err := NewToken(s.random)
	if err != nil {
		return Issued{}, err
	}
	now := s.now().UTC()
	claim := Claim{Subdomain: subdomain, TokenHash: HashToken(token), Revision: 1, CreatedAt: now}
	entry := AuditEntry{At: now, Subdomain: subdomain, Kind: AuditClaimed, Actor: s.actor, Detail: "revision 1"}
	if err := s.store.InsertClaim(ctx, claim, entry); err != nil {
		return Issued{}, err
	}
	return Issued{Claim: claim, Token: token}, nil
}

// Revoke withdraws a claim's credential, closes its live link if any, and records both in the
// same transaction as the revision change.
func (s *Service) Revoke(ctx context.Context, subdomain string, expectedRevision int) (Claim, error) {
	claim, err := s.store.ClaimBySubdomain(ctx, subdomain)
	if err != nil {
		return Claim{}, err
	}
	if claim.Revision != expectedRevision {
		return Claim{}, fmt.Errorf("%w: expected revision %d, current revision is %d", ErrConflict, expectedRevision, claim.Revision)
	}
	outcome := Transition(Status{Claim: claim.State(), Presence: s.registry.Presence(subdomain)}, EventRevoke)
	if outcome.Err != nil {
		return Claim{}, outcome.Err
	}
	now := s.now().UTC()
	entries := s.entries(outcome.Audit, subdomain, now, s.actor, fmt.Sprintf("revision %d", claim.Revision+1))
	revoked, err := s.store.RevokeClaim(ctx, subdomain, expectedRevision, now, entries)
	if err != nil {
		return Claim{}, err
	}
	link, had := s.registry.Evict(subdomain)
	if had {
		s.closeLink(link, subdomain, CloseRevoked)
	}
	return revoked, nil
}

// Authenticate resolves an agent credential to its active claim without attaching anything.
func (s *Service) Authenticate(ctx context.Context, token string) (Claim, error) {
	if err := ValidateToken(token); err != nil {
		return Claim{}, err
	}
	claim, err := s.store.ClaimByTokenHash(ctx, HashToken(token))
	if errors.Is(err, ErrNotFound) {
		return Claim{}, fmt.Errorf("%w: unknown agent token", ErrUnauthorized)
	}
	if err != nil {
		return Claim{}, err
	}
	if claim.Revoked {
		return Claim{}, fmt.Errorf("%w: agent token was revoked", ErrUnauthorized)
	}
	return claim, nil
}

// Connection is one attached link's handle; Lost reports its end.
type Connection struct {
	Subdomain  string
	generation uint64
	service    *Service
}

// Attach installs link as the server of claim's subdomain. The claim is re-read so a revocation
// that raced the upgrade is honored, and an earlier link is superseded and closed.
func (s *Service) Attach(ctx context.Context, claim Claim, link Link) (Connection, error) {
	if link == nil {
		return Connection{}, errors.New("link is required")
	}
	current, err := s.store.ClaimBySubdomain(ctx, claim.Subdomain)
	if err != nil {
		return Connection{}, err
	}
	if current.TokenHash != claim.TokenHash {
		return Connection{}, fmt.Errorf("%w: claim was reissued", ErrUnauthorized)
	}
	outcome := Transition(Status{Claim: current.State(), Presence: s.registry.Presence(claim.Subdomain)}, EventConnect)
	if outcome.Err != nil {
		return Connection{}, outcome.Err
	}
	generation, previous := s.registry.Attach(claim.Subdomain, link)
	if previous != nil {
		s.closeLink(previous, claim.Subdomain, CloseSuperseded)
	}
	entries := s.entries(outcome.Audit, claim.Subdomain, s.now().UTC(), AgentActor(claim.Subdomain), fmt.Sprintf("generation %d", generation))
	if err := s.store.AppendAudit(ctx, entries); err != nil {
		s.registry.Detach(claim.Subdomain, generation)
		return Connection{}, err
	}
	return Connection{Subdomain: claim.Subdomain, generation: generation, service: s}, nil
}

// Lost records that this connection's link ended. A superseded connection's loss is ignored
// because a newer generation now serves the subdomain.
func (c Connection) Lost(ctx context.Context) error {
	if c.service == nil {
		return nil
	}
	if !c.service.registry.Detach(c.Subdomain, c.generation) {
		return nil
	}
	outcome := Transition(Status{Claim: ClaimActive, Presence: PresenceLive}, EventLinkLost)
	entries := c.service.entries(outcome.Audit, c.Subdomain, c.service.now().UTC(), AgentActor(c.Subdomain), fmt.Sprintf("generation %d", c.generation))
	return c.service.store.AppendAudit(ctx, entries)
}

// View is one tunnel as the read plane sees it: durable claim state joined with presence.
type View struct {
	Subdomain string
	Revision  int
	State     ClaimState
	Presence  Presence
	CreatedAt time.Time
	RevokedAt time.Time
}

// Tunnels lists every claim with its current presence and without any credential material.
func (s *Service) Tunnels(ctx context.Context) ([]View, error) {
	claims, err := s.store.ListClaims(ctx)
	if err != nil {
		return nil, err
	}
	views := make([]View, 0, len(claims))
	for _, claim := range claims {
		views = append(views, View{
			Subdomain: claim.Subdomain,
			Revision:  claim.Revision,
			State:     claim.State(),
			Presence:  s.registry.Presence(claim.Subdomain),
			CreatedAt: claim.CreatedAt,
			RevokedAt: claim.RevokedAt,
		})
	}
	return views, nil
}

func (s *Service) entries(kinds []AuditKind, subdomain string, at time.Time, actor, detail string) []AuditEntry {
	entries := make([]AuditEntry, 0, len(kinds))
	for _, kind := range kinds {
		entries = append(entries, AuditEntry{At: at, Subdomain: subdomain, Kind: kind, Actor: actor, Detail: detail})
	}
	return entries
}

func (s *Service) closeLink(link Link, subdomain string, reason CloseReason) {
	if err := link.Close(reason); err != nil {
		s.logger.Warn("close tunnel link", "subdomain", subdomain, "reason", reason, "error", err)
	}
}
