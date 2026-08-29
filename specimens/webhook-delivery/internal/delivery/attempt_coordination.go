package delivery

import "context"

// AttemptCoordinator prevents endpoint disablement from overtaking an active send and audit commit.
type AttemptCoordinator struct {
	permit chan struct{}
}

// NewAttemptCoordinator creates the single-worker coordination boundary.
func NewAttemptCoordinator() *AttemptCoordinator {
	permit := make(chan struct{}, 1)
	permit <- struct{}{}
	return &AttemptCoordinator{permit: permit}
}

func (c *AttemptCoordinator) run(ctx context.Context, action func() error) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.permit:
	}
	defer func() { c.permit <- struct{}{} }()
	return action()
}
