package oncall

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func mustTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func weeklySchedule(t *testing.T) Schedule {
	t.Helper()
	return Schedule{
		Layers: []Layer{{
			Name:       "weekly",
			Start:      mustTime(t, "2026-01-05T09:00:00Z"),
			Rotation:   Duration(7 * 24 * time.Hour),
			Responders: []string{"alice", "bob"},
		}},
		Overrides: []Override{{
			Responder: "carol",
			Start:     mustTime(t, "2026-01-13T09:00:00Z"),
			End:       mustTime(t, "2026-01-14T09:00:00Z"),
		}},
	}
}

func TestOnCallRotatesFromLayerStart(t *testing.T) {
	schedule := weeklySchedule(t)
	if err := schedule.Validate(); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		at   string
		want string
		ok   bool
	}{
		{"2026-01-05T08:59:59Z", "", false},
		{"2026-01-05T09:00:00Z", "alice", true},
		{"2026-01-12T08:59:59Z", "alice", true},
		{"2026-01-12T09:00:00Z", "bob", true},
		{"2026-01-19T09:00:00Z", "alice", true},
	}
	for _, testCase := range cases {
		got, ok := schedule.OnCall(mustTime(t, testCase.at))
		if got != testCase.want || ok != testCase.ok {
			t.Fatalf("at %s: got %q/%t want %q/%t", testCase.at, got, ok, testCase.want, testCase.ok)
		}
	}
}

func TestOverrideWinsOnlyInsideItsWindow(t *testing.T) {
	schedule := weeklySchedule(t)
	inside, _ := schedule.OnCall(mustTime(t, "2026-01-13T12:00:00Z"))
	if inside != "carol" {
		t.Fatalf("override should win inside its window, got %q", inside)
	}
	atEnd, _ := schedule.OnCall(mustTime(t, "2026-01-14T09:00:00Z"))
	if atEnd != "bob" {
		t.Fatalf("override end is exclusive, got %q", atEnd)
	}
}

func TestHigherLayerOverridesLowerLayer(t *testing.T) {
	schedule := weeklySchedule(t)
	schedule.Layers = append(schedule.Layers, Layer{
		Name:       "weekend",
		Start:      mustTime(t, "2026-01-10T09:00:00Z"),
		End:        mustTime(t, "2026-01-12T09:00:00Z"),
		Rotation:   Duration(24 * time.Hour),
		Responders: []string{"dave"},
	})
	if err := schedule.Validate(); err != nil {
		t.Fatal(err)
	}
	got, _ := schedule.OnCall(mustTime(t, "2026-01-11T00:00:00Z"))
	if got != "dave" {
		t.Fatalf("top layer should win, got %q", got)
	}
	after, _ := schedule.OnCall(mustTime(t, "2026-01-12T10:00:00Z"))
	if after != "bob" {
		t.Fatalf("expired layer should not apply, got %q", after)
	}
}

func TestValidateRejectsBrokenDeclarations(t *testing.T) {
	base := weeklySchedule(t)
	cases := map[string]func(*Schedule){
		"no layers":           func(s *Schedule) { s.Layers = nil },
		"short rotation":      func(s *Schedule) { s.Layers[0].Rotation = Duration(time.Second) },
		"bad responder":       func(s *Schedule) { s.Layers[0].Responders = []string{"Alice"} },
		"end before start":    func(s *Schedule) { s.Layers[0].End = s.Layers[0].Start },
		"override inverted":   func(s *Schedule) { s.Overrides[0].End = s.Overrides[0].Start },
		"overlapping windows": func(s *Schedule) { s.Overrides = append(s.Overrides, s.Overrides[0]) },
		"duplicate layer":     func(s *Schedule) { s.Layers = append(s.Layers, s.Layers[0]) },
	}
	for name, mutate := range cases {
		schedule := weeklySchedule(t)
		schedule.Layers = append([]Layer(nil), base.Layers...)
		schedule.Overrides = append([]Override(nil), base.Overrides...)
		mutate(&schedule)
		if err := schedule.Validate(); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s: expected ErrInvalid, got %v", name, err)
		}
	}
}

func TestRespondersAreSortedAndUnique(t *testing.T) {
	schedule := weeklySchedule(t)
	got := schedule.Responders()
	want := []string{"alice", "bob", "carol"}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}

func TestDurationJSONRoundTrip(t *testing.T) {
	raw := `{"layers":[{"name":"l","start":"2026-01-05T09:00:00Z","rotation":"168h","responders":["alice"]}]}`
	var schedule Schedule
	if err := json.Unmarshal([]byte(raw), &schedule); err != nil {
		t.Fatal(err)
	}
	if time.Duration(schedule.Layers[0].Rotation) != 7*24*time.Hour {
		t.Fatalf("rotation parsed as %s", time.Duration(schedule.Layers[0].Rotation))
	}
	encoded, err := json.Marshal(schedule.Layers[0].Rotation)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != `"168h0m0s"` {
		t.Fatalf("unexpected encoding %s", encoded)
	}
	var bad Duration
	if err := json.Unmarshal([]byte(`168`), &bad); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected ErrInvalid for unquoted duration, got %v", err)
	}
}
