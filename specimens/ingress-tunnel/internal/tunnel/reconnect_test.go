package tunnel

import (
	"testing"
	"time"
)

func TestScheduleCapsAtTheLastDelay(t *testing.T) {
	schedule, err := NewSchedule([]time.Duration{time.Second, 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	for attempt, want := range map[int]time.Duration{-1: time.Second, 0: time.Second, 1: time.Second, 2: 3 * time.Second, 3: 3 * time.Second, 99: 3 * time.Second} {
		if got := schedule.Delay(attempt); got != want {
			t.Fatalf("attempt %d delay = %s, want %s", attempt, got, want)
		}
	}
}

func TestScheduleRejectsEmptyOrNonPositiveDelays(t *testing.T) {
	if _, err := NewSchedule(nil); err == nil {
		t.Fatal("empty schedule accepted")
	}
	if _, err := NewSchedule([]time.Duration{time.Second, 0}); err == nil {
		t.Fatal("zero delay accepted")
	}
}

func TestDefaultReconnectDelaysAreAValidSchedule(t *testing.T) {
	if _, err := NewSchedule(DefaultReconnectDelays); err != nil {
		t.Fatal(err)
	}
}
