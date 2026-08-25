package flags

import "context"

// Audit returns the most recent publication entries first.
func (s *Service) Audit(ctx context.Context, limit int) ([]AuditEntry, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	return s.store.Audit(ctx, limit)
}
