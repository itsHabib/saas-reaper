package incident

import (
	"context"
	"errors"
	"fmt"
)

// TimerActor is the audit principal for transitions driven by the durable timer.
const TimerActor = "system:escalation-timer"

// EscalateDue fires every armed timer whose instant has passed, one bounded batch at a time.
//
// The timer is the incident row itself, so nothing is held in memory between
// ticks or across restarts. Each incident is a separate revision-checked
// transition; a lost race is skipped and every other due incident still fires.
func (d *Desk) EscalateDue(ctx context.Context, limit int) (int, error) {
	if limit < 1 || limit > 100 {
		return 0, fmt.Errorf("%w: due limit must be between 1 and 100", ErrInvalid)
	}
	due, err := d.store.DueEscalations(ctx, d.now().UTC(), limit)
	if err != nil {
		return 0, fmt.Errorf("load due escalations: %w", err)
	}
	var failures []error
	fired := 0
	for _, current := range due {
		err := d.signal(ctx, current, SignalTimeout, TimerActor)
		if errors.Is(err, ErrConflict) {
			continue
		}
		if err != nil {
			failures = append(failures, fmt.Errorf("escalate incident %s: %w", current.ID, err))
			continue
		}
		fired++
	}
	return fired, errors.Join(failures...)
}
