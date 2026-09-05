package api

import (
	"fmt"

	"github.com/itsHabib/saas-reaper/specimens/ingress-tunnel/internal/tunnel"
)

func tunnelInvalid(message string) error {
	return fmt.Errorf("%w: %s", tunnel.ErrInvalid, message)
}
