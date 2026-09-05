package tunnel

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
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

// SupersedeCooldown is how long after one agent takes a claim over that a further takeover is
// refused. The close frame that tells the loser to stop is best effort; if it is missed, the
// loser reconnects on its schedule and would otherwise take the claim straight back. Within
// the cooldown a connect against a live link is a conflict, which the agent treats as final.
const SupersedeCooldown = time.Minute

// Service composes the store and the routing table under the lifecycle table. One mutex
// sequences every status change, so the durable claim and the volatile presence are always
// read and changed as a pair and the audit describes exactly the transition that happened.
type Service struct {
	mu         sync.Mutex
	generation uint64
	superseded map[string]time.Time
	store      Store
	registry   *Registry
	actor      string
	now        func() time.Time
	random     io.Reader
	logger     *slog.Logger
}

// NewService validates the composition. actor is the authenticated management principal.
func NewService(store Store, registry *Registry, actor string, now func() time.Time, random io.Reader, logger *slog.Logger) (*Service, error) {
	if store == nil || registry == nil || now == nil || logger == nil {
		return nil, errors.New("store, registry, clock, and logger are required")
	}
	if strings.TrimSpace(actor) == "" {
		return nil, errors.New("management actor is required")
	}
	return &Service{
		superseded: map[string]time.Time{},
		store:      store,
		registry:   registry,
		actor:      actor,
		now:        now,
		random:     random,
		logger:     logger,
	}, nil
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
		return Issued{}, fmt.Errorf("claim %s: %w", subdomain, err)
	}
	return Issued{Claim: claim, Token: token}, nil
}

// Revoke withdraws a claim's credential, closes its live link if any, and records both in the
// same transaction as the revision change.
func (s *Service) Revoke(ctx context.Context, subdomain string, expectedRevision int) (Claim, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	claim, err := s.store.ClaimBySubdomain(ctx, subdomain)
	if err != nil {
		return Claim{}, fmt.Errorf("revoke %s: %w", subdomain, err)
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
		return Claim{}, fmt.Errorf("revoke %s: %w", subdomain, err)
	}
	link, had := s.registry.Evict(subdomain)
	if had {
		delete(s.superseded, subdomain)
		s.closeLink(link, subdomain, CloseRevoked)
	}
	return revoked, nil
}

// Authenticate resolves an agent credential to its active claim without attaching anything.
// It also refuses, before any upgrade, a takeover that the cooldown forbids.
func (s *Service) Authenticate(ctx context.Context, token string) (Claim, error) {
	if err := ValidateToken(token); err != nil {
		return Claim{}, err
	}
	claim, err := s.store.ClaimByTokenHash(ctx, HashToken(token))
	if errors.Is(err, ErrNotFound) {
		return Claim{}, fmt.Errorf("%w: unknown agent token", ErrUnauthorized)
	}
	if err != nil {
		return Claim{}, fmt.Errorf("authenticate agent: %w", err)
	}
	if claim.Revoked {
		return Claim{}, fmt.Errorf("%w: agent token was revoked", ErrUnauthorized)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return claim, s.takeoverAllowed(claim.Subdomain)
}

// takeoverAllowed is called under the lock: a connect against a live link is fine unless that
// link itself arrived by supersession within the cooldown.
func (s *Service) takeoverAllowed(subdomain string) error {
	if s.registry.Presence(subdomain) != PresenceLive {
		return nil
	}
	since, ok := s.superseded[subdomain]
	if !ok || s.now().Sub(since) >= SupersedeCooldown {
		return nil
	}
	return fmt.Errorf("%w: another agent took over %s recently; try again after the cooldown", ErrConflict, subdomain)
}

// Connection is one attached link's handle; Lost reports its end.
type Connection struct {
	Subdomain  string
	generation uint64
	service    *Service
}

// Attach installs link as the server of claim's subdomain. Under the lock the claim is re-read,
// so a revocation that raced the upgrade is honored; the audit is committed before the routing
// table changes, so a failed write evicts nobody and records nothing; and only then is an
// earlier link superseded and closed.
func (s *Service) Attach(ctx context.Context, claim Claim, link Link) (Connection, error) {
	if link == nil {
		return Connection{}, errors.New("link is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := s.store.ClaimBySubdomain(ctx, claim.Subdomain)
	if err != nil {
		return Connection{}, fmt.Errorf("attach %s: %w", claim.Subdomain, err)
	}
	if current.TokenHash != claim.TokenHash {
		return Connection{}, fmt.Errorf("%w: claim was reissued", ErrUnauthorized)
	}
	if err := s.takeoverAllowed(claim.Subdomain); err != nil {
		return Connection{}, err
	}
	outcome := Transition(Status{Claim: current.State(), Presence: s.registry.Presence(claim.Subdomain)}, EventConnect)
	if outcome.Err != nil {
		return Connection{}, outcome.Err
	}
	generation := s.generation + 1
	entries := s.entries(outcome.Audit, claim.Subdomain, s.now().UTC(), AgentActor(claim.Subdomain), fmt.Sprintf("generation %d", generation))
	if err := s.store.AppendAudit(ctx, entries); err != nil {
		return Connection{}, fmt.Errorf("attach %s: %w", claim.Subdomain, err)
	}
	s.generation = generation
	previous := s.registry.Attach(claim.Subdomain, link, generation)
	if previous != nil {
		s.superseded[claim.Subdomain] = s.now()
		s.closeLink(previous, claim.Subdomain, CloseSuperseded)
	}
	return Connection{Subdomain: claim.Subdomain, generation: generation, service: s}, nil
}

// Lost records that this connection's link ended. A superseded or evicted connection's loss is
// ignored because the table no longer holds its generation.
func (c Connection) Lost(ctx context.Context) error {
	if c.service == nil {
		return nil
	}
	s := c.service
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.registry.Detach(c.Subdomain, c.generation) {
		return nil
	}
	delete(s.superseded, c.Subdomain)
	outcome := Transition(Status{Claim: ClaimActive, Presence: PresenceLive}, EventLinkLost)
	entries := s.entries(outcome.Audit, c.Subdomain, s.now().UTC(), AgentActor(c.Subdomain), fmt.Sprintf("generation %d", c.generation))
	if err := s.store.AppendAudit(ctx, entries); err != nil {
		return fmt.Errorf("record disconnect %s: %w", c.Subdomain, err)
	}
	return nil
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
		return nil, fmt.Errorf("list tunnels: %w", err)
	}
	live := s.registry.Live()
	views := make([]View, 0, len(claims))
	for _, claim := range claims {
		presence := PresenceAbsent
		if _, ok := live[claim.Subdomain]; ok {
			presence = PresenceLive
		}
		views = append(views, View{
			Subdomain: claim.Subdomain,
			Revision:  claim.Revision,
			State:     claim.State(),
			Presence:  presence,
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
