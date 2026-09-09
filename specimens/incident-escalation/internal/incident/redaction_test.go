package incident

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"
)

// A transport error names the destination it failed to reach. The audit-read
// token is lower authority than the principal that configured that destination,
// so nothing but a classification the transport chose may be persisted.
func TestRawTransportErrorsNeverReachTheAudit(t *testing.T) {
	schedule, err := NewRetrySchedule([]time.Duration{time.Second})
	if err != nil {
		t.Fatal(err)
	}
	pending := Notification{ID: "ntf_1", State: NotificationPending}
	leaky := &url.Error{
		Op:  "Post",
		URL: "https://pager.example/hook?token=super-secret",
		Err: errors.New("dial tcp 10.1.2.3:443: connect: connection refused"),
	}
	cases := map[string]struct {
		sendErr error
		want    string
	}{
		"unclassified url error": {sendErr: fmt.Errorf("post page: %w", leaky), want: unclassifiedFailure},
		"unclassified smtp text": {
			sendErr: errors.New("550 5.1.1 <ada@example.test>: Recipient address rejected by mail-07.example"),
			want:    unclassifiedFailure,
		},
		"classified transport": {sendErr: NewPageError("connection_failed", false), want: "connection_failed"},
		"classified status":    {sendErr: NewPageError("http_status_503", false), want: "http_status_503"},
		"classified permanent": {sendErr: NewPageError("relay_unconfigured", true), want: "relay_unconfigured"},
		"wrapped classification": {
			sendErr: fmt.Errorf("paging %s: %w", "ada", NewPageError("smtp_status_550", false)),
			want:    "smtp_status_550",
		},
	}
	for name, testCase := range cases {
		attempt := schedule.Resolve(pending, testCase.sendErr, time.Unix(10, 0), time.Unix(11, 0))
		if attempt.Error != testCase.want {
			t.Fatalf("%s: audited %q, want %q", name, attempt.Error, testCase.want)
		}
		for _, secret := range []string{"pager.example", "super-secret", "10.1.2.3", "ada@example.test", "mail-07"} {
			if strings.Contains(attempt.Error, secret) {
				t.Fatalf("%s: the audit leaked %q in %q", name, secret, attempt.Error)
			}
		}
	}
}

func TestPageErrorRendersOnlyItsClassification(t *testing.T) {
	failure := NewPageError("connection_failed", false)
	if failure.Error() != "connection_failed" {
		t.Fatalf("a page error must render only its reason, got %q", failure.Error())
	}
	if errors.Is(failure, ErrPermanent) {
		t.Fatal("a retryable failure must not report itself as permanent")
	}
	permanent := NewPageError("relay_unconfigured", true)
	if !errors.Is(permanent, ErrPermanent) {
		t.Fatal("a permanent failure must satisfy errors.Is(err, ErrPermanent)")
	}
	oversized := NewPageError(strings.Repeat("x", maxReasonBytes+1), false)
	if oversized.Reason != unclassifiedFailure {
		t.Fatalf("an oversized reason must be replaced, got %q", oversized.Reason)
	}
	if NewPageError("", false).Reason != unclassifiedFailure {
		t.Fatal("an empty reason must be replaced")
	}
}

func TestPermanentClassificationStillEndsTheSchedule(t *testing.T) {
	schedule, err := NewRetrySchedule([]time.Duration{time.Second, time.Second})
	if err != nil {
		t.Fatal(err)
	}
	pending := Notification{ID: "ntf_1", State: NotificationPending}
	attempt := schedule.Resolve(pending, NewPageError("http_status_404", true), time.Unix(10, 0), time.Unix(10, 0))
	if attempt.Outcome != OutcomeFailed || attempt.State != NotificationFailed || !attempt.NextAttemptAt.IsZero() {
		t.Fatalf("a permanent classification must be terminal: %#v", attempt)
	}
	retryable := schedule.Resolve(pending, NewPageError("http_status_503", false), time.Unix(10, 0), time.Unix(10, 0))
	if retryable.Outcome != OutcomeRetrying || retryable.NextAttemptAt.IsZero() {
		t.Fatalf("a retryable classification must schedule another attempt: %#v", retryable)
	}
}

// A lost optimistic race must be re-read and re-applied. Dropping the event
// would lose an acknowledge or a resolve, and the sender treats the conflict
// status as final.
func TestIngestReappliesAfterRepeatedLostRaces(t *testing.T) {
	desk, store, _ := seededDesk(t, 0)
	ctx := context.Background()
	if _, err := desk.Ingest(ctx, triggerAlert("racy")); err != nil {
		t.Fatal(err)
	}
	store.failTransition = ingestAttempts - 1
	resolve := Alert{RoutingKey: "rk-checkout", Action: ActionResolve, DedupKey: "racy"}
	if _, err := desk.Ingest(ctx, resolve); err != nil {
		t.Fatalf("a resolve must survive repeated lost races: %v", err)
	}
	if _, err := store.OpenIncident(ctx, "checkout", "racy"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("the resolve must have closed the incident, got %v", err)
	}
}

func TestIngestReportsConflictOnlyWhenTheBoundIsExhausted(t *testing.T) {
	desk, store, _ := seededDesk(t, 0)
	ctx := context.Background()
	if _, err := desk.Ingest(ctx, triggerAlert("racy")); err != nil {
		t.Fatal(err)
	}
	store.failTransition = ingestAttempts + 1
	resolve := Alert{RoutingKey: "rk-checkout", Action: ActionResolve, DedupKey: "racy"}
	if _, err := desk.Ingest(ctx, resolve); !errors.Is(err, ErrConflict) {
		t.Fatalf("an exhausted bound must report a conflict the caller can retry, got %v", err)
	}
}
