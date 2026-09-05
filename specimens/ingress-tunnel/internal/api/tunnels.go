package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/itsHabib/saas-reaper/specimens/ingress-tunnel/internal/tunnel"
)

type claimRequest struct {
	Subdomain string `json:"subdomain"`
}

type claimResponse struct {
	Subdomain string    `json:"subdomain"`
	Token     string    `json:"token"`
	Revision  int       `json:"revision"`
	CreatedAt time.Time `json:"createdAt"`
}

type revokeRequest struct {
	ExpectedRevision int `json:"expectedRevision"`
}

type tunnelResponse struct {
	Subdomain string     `json:"subdomain"`
	Revision  int        `json:"revision"`
	State     string     `json:"state"`
	Presence  string     `json:"presence"`
	CreatedAt time.Time  `json:"createdAt"`
	RevokedAt *time.Time `json:"revokedAt,omitempty"`
}

type auditResponse struct {
	Sequence  int64     `json:"sequence"`
	At        time.Time `json:"at"`
	Subdomain string    `json:"subdomain"`
	Kind      string    `json:"kind"`
	Actor     string    `json:"actor"`
	Detail    string    `json:"detail"`
}

// claimTunnel issues a credential exactly once; the response is the only time it is visible.
func (s *Server) claimTunnel(w http.ResponseWriter, r *http.Request) {
	if !requireJSON(w, r) {
		return
	}
	var request claimRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	issued, err := s.tunnels.Claim(r.Context(), request.Subdomain)
	if err != nil {
		writePolicyError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, claimResponse{
		Subdomain: issued.Claim.Subdomain,
		Token:     issued.Token,
		Revision:  issued.Claim.Revision,
		CreatedAt: issued.Claim.CreatedAt,
	})
}

func (s *Server) revokeTunnel(w http.ResponseWriter, r *http.Request) {
	if !requireJSON(w, r) {
		return
	}
	var request revokeRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	claim, err := s.tunnels.Revoke(r.Context(), r.PathValue("subdomain"), request.ExpectedRevision)
	if err != nil {
		writePolicyError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, viewResponse(tunnel.View{
		Subdomain: claim.Subdomain,
		Revision:  claim.Revision,
		State:     claim.State(),
		Presence:  tunnel.PresenceAbsent,
		CreatedAt: claim.CreatedAt,
		RevokedAt: claim.RevokedAt,
	}))
}

func (s *Server) listTunnels(w http.ResponseWriter, r *http.Request) {
	views, err := s.tunnels.Tunnels(r.Context())
	if err != nil {
		writePolicyError(w, err)
		return
	}
	body := make([]tunnelResponse, 0, len(views))
	for _, view := range views {
		body = append(body, viewResponse(view))
	}
	writeJSON(w, http.StatusOK, map[string][]tunnelResponse{"tunnels": body})
}

func (s *Server) listAudit(w http.ResponseWriter, r *http.Request) {
	limit, err := auditLimit(r.URL.Query().Get("limit"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	entries, err := s.audit.Audit(r.Context(), limit)
	if err != nil {
		writePolicyError(w, err)
		return
	}
	body := make([]auditResponse, 0, len(entries))
	for _, entry := range entries {
		body = append(body, auditResponse{
			Sequence:  entry.Sequence,
			At:        entry.At,
			Subdomain: entry.Subdomain,
			Kind:      string(entry.Kind),
			Actor:     entry.Actor,
			Detail:    entry.Detail,
		})
	}
	writeJSON(w, http.StatusOK, map[string][]auditResponse{"audit": body})
}

func auditLimit(raw string) (int, error) {
	if raw == "" {
		return 50, nil
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit < 1 || limit > 1000 {
		return 0, tunnelInvalid("limit must be an integer between 1 and 1000")
	}
	return limit, nil
}

func viewResponse(view tunnel.View) tunnelResponse {
	response := tunnelResponse{
		Subdomain: view.Subdomain,
		Revision:  view.Revision,
		State:     string(view.State),
		Presence:  string(view.Presence),
		CreatedAt: view.CreatedAt,
	}
	if !view.RevokedAt.IsZero() {
		revokedAt := view.RevokedAt
		response.RevokedAt = &revokedAt
	}
	return response
}
