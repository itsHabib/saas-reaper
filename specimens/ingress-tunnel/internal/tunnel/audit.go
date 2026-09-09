package tunnel

import "time"

// AuditEntry is one append-only lifecycle row. Sequence is assigned by the store.
type AuditEntry struct {
	Sequence  int64
	At        time.Time
	Subdomain string
	Kind      AuditKind
	Actor     string
	Detail    string
}

// AgentActor names the principal behind link events: the claim the credential proved.
func AgentActor(subdomain string) string {
	return "agent:" + subdomain
}
