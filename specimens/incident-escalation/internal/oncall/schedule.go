// Package oncall resolves declared rotations and overrides to one responder at a time.
package oncall

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// ErrInvalid reports a schedule declaration that violates on-call policy.
var ErrInvalid = errors.New("invalid on-call schedule")

const (
	maxLayers     = 16
	maxOverrides  = 256
	maxResponders = 64
	minRotation   = time.Minute
	maxRotation   = 366 * 24 * time.Hour
)

var responderPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// Layer rotates responders on a fixed cadence from a start instant.
type Layer struct {
	Name       string    `json:"name"`
	Start      time.Time `json:"start"`
	End        time.Time `json:"end,omitempty"`
	Rotation   Duration  `json:"rotation"`
	Responders []string  `json:"responders"`
}

// Override replaces every layer with one responder inside a half-open window.
type Override struct {
	Responder string    `json:"responder"`
	Start     time.Time `json:"start"`
	End       time.Time `json:"end"`
}

// Schedule is the declared data format: ordered layers plus non-overlapping overrides.
type Schedule struct {
	Layers    []Layer    `json:"layers"`
	Overrides []Override `json:"overrides,omitempty"`
}

// Validate rejects declarations that cannot resolve deterministically.
func (s Schedule) Validate() error {
	if len(s.Layers) == 0 || len(s.Layers) > maxLayers {
		return fmt.Errorf("%w: between 1 and %d layers are required", ErrInvalid, maxLayers)
	}
	if len(s.Overrides) > maxOverrides {
		return fmt.Errorf("%w: at most %d overrides are allowed", ErrInvalid, maxOverrides)
	}
	names := map[string]bool{}
	for index, layer := range s.Layers {
		if err := layer.validate(); err != nil {
			return fmt.Errorf("layer %d: %w", index, err)
		}
		if names[layer.Name] {
			return fmt.Errorf("%w: layer name %q is declared twice", ErrInvalid, layer.Name)
		}
		names[layer.Name] = true
	}
	return validateOverrides(s.Overrides)
}

func (l Layer) validate() error {
	if strings.TrimSpace(l.Name) == "" || l.Name != strings.TrimSpace(l.Name) {
		return fmt.Errorf("%w: layer name is required", ErrInvalid)
	}
	if l.Start.IsZero() {
		return fmt.Errorf("%w: layer start is required", ErrInvalid)
	}
	if !l.End.IsZero() && !l.End.After(l.Start) {
		return fmt.Errorf("%w: layer end must follow its start", ErrInvalid)
	}
	rotation := time.Duration(l.Rotation)
	if rotation < minRotation || rotation > maxRotation {
		return fmt.Errorf("%w: layer rotation must be between %s and %s", ErrInvalid, minRotation, maxRotation)
	}
	if len(l.Responders) == 0 || len(l.Responders) > maxResponders {
		return fmt.Errorf("%w: a layer names between 1 and %d responders", ErrInvalid, maxResponders)
	}
	for _, responder := range l.Responders {
		if !responderPattern.MatchString(responder) {
			return fmt.Errorf("%w: responder %q must be a lowercase slug", ErrInvalid, responder)
		}
	}
	return nil
}

func validateOverrides(overrides []Override) error {
	ordered := append([]Override(nil), overrides...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Start.Before(ordered[j].Start) })
	for index, override := range ordered {
		if !responderPattern.MatchString(override.Responder) {
			return fmt.Errorf("%w: override responder %q must be a lowercase slug", ErrInvalid, override.Responder)
		}
		if override.Start.IsZero() || !override.End.After(override.Start) {
			return fmt.Errorf("%w: override windows need a start before their end", ErrInvalid)
		}
		if index > 0 && ordered[index-1].End.After(override.Start) {
			return fmt.Errorf("%w: override windows must not overlap", ErrInvalid)
		}
	}
	return nil
}

// Responders lists every responder the schedule can ever resolve to, sorted and unique.
func (s Schedule) Responders() []string {
	set := map[string]bool{}
	for _, layer := range s.Layers {
		for _, responder := range layer.Responders {
			set[responder] = true
		}
	}
	for _, override := range s.Overrides {
		set[override.Responder] = true
	}
	responders := make([]string, 0, len(set))
	for responder := range set {
		responders = append(responders, responder)
	}
	sort.Strings(responders)
	return responders
}

// OnCall resolves who holds the pager at one instant; false means nobody is scheduled.
//
// An override covering the instant wins. Otherwise the highest-index layer whose
// window covers the instant wins, rotating through its responders from its start.
func (s Schedule) OnCall(at time.Time) (string, bool) {
	for _, override := range s.Overrides {
		if covers(override.Start, override.End, at) {
			return override.Responder, true
		}
	}
	for index := len(s.Layers) - 1; index >= 0; index-- {
		layer := s.Layers[index]
		if !covers(layer.Start, layer.End, at) {
			continue
		}
		elapsed := at.Sub(layer.Start)
		slot := int(elapsed / time.Duration(layer.Rotation))
		return layer.Responders[slot%len(layer.Responders)], true
	}
	return "", false
}

func covers(start, end, at time.Time) bool {
	if at.Before(start) {
		return false
	}
	if end.IsZero() {
		return true
	}
	return at.Before(end)
}

// Duration is a Go duration literal such as "168h" inside the JSON declaration.
type Duration time.Duration

// MarshalJSON renders the duration as its Go literal.
func (d Duration) MarshalJSON() ([]byte, error) {
	return []byte(`"` + time.Duration(d).String() + `"`), nil
}

// UnmarshalJSON parses a quoted Go duration literal.
func (d *Duration) UnmarshalJSON(raw []byte) error {
	if len(raw) < 2 || raw[0] != '"' || raw[len(raw)-1] != '"' {
		return fmt.Errorf("%w: duration must be a quoted literal such as \"168h\"", ErrInvalid)
	}
	parsed, err := time.ParseDuration(string(raw[1 : len(raw)-1]))
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	*d = Duration(parsed)
	return nil
}
