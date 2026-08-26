package flags

import (
	"errors"
	"testing"
)

func TestEvaluatePrecedenceAndReasons(t *testing.T) {
	flag := boolFlag()
	flag.Rules = []Rule{{Attribute: "organization.id", Equals: "acme", Variant: "on"}}
	flag.Rollout = &Rollout{Attribute: "targetingKey", Percentage: 30, Variant: "on"}
	tests := []struct {
		name    string
		mutate  func(*Flag)
		context map[string]any
		variant string
		reason  string
	}{
		{
			name: "disabled wins over targeting",
			mutate: func(flag *Flag) {
				flag.Enabled = false
			},
			context: map[string]any{"targetingKey": "user-1", "organization.id": "acme"},
			variant: "off",
			reason:  "DISABLED",
		},
		{
			name:    "ordered rule wins over rollout",
			context: map[string]any{"targetingKey": "user-2", "organization.id": "acme"},
			variant: "on",
			reason:  "TARGETING_MATCH",
		},
		{
			name:    "rollout includes stable bucket",
			context: map[string]any{"targetingKey": "user-1", "organization.id": "other"},
			variant: "on",
			reason:  "SPLIT",
		},
		{
			name:    "rollout excludes stable bucket",
			context: map[string]any{"targetingKey": "user-2", "organization.id": "other"},
			variant: "off",
			reason:  "STATIC",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := flag.Copy()
			if test.mutate != nil {
				test.mutate(&candidate)
			}
			got, err := evaluate(candidate, test.context)
			if err != nil {
				t.Fatalf("evaluate: %v", err)
			}
			if got.Variant != test.variant || got.Reason != test.reason {
				t.Fatalf("got variant=%q reason=%q, want variant=%q reason=%q", got.Variant, got.Reason, test.variant, test.reason)
			}
		})
	}
}

func TestRolloutGoldenBuckets(t *testing.T) {
	rollout := Rollout{Attribute: "targetingKey", Percentage: 30, Variant: "on"}
	if !inRollout("checkout-v2", rollout, map[string]any{"targetingKey": "user-1"}) {
		t.Fatal("user-1 must remain inside the 30 percent golden bucket")
	}
	if inRollout("checkout-v2", rollout, map[string]any{"targetingKey": "user-2"}) {
		t.Fatal("user-2 must remain outside the 30 percent golden bucket")
	}
}

func TestValidateRejectsDomainAndShapeViolations(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Flag)
	}{
		{
			name: "unknown targeting attribute",
			mutate: func(flag *Flag) {
				flag.Rules = []Rule{{Attribute: "email", Equals: "person@example.com", Variant: "on"}}
			},
		},
		{
			name: "wrong variant value kind",
			mutate: func(flag *Flag) {
				flag.Variants["on"] = "yes"
			},
		},
		{
			name: "undefined rollout variant",
			mutate: func(flag *Flag) {
				flag.Rollout = &Rollout{Attribute: "targetingKey", Percentage: 10, Variant: "missing"}
			},
		},
		{
			name: "unsupported key character",
			mutate: func(flag *Flag) {
				flag.Key = "checkout/v2"
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			flag := boolFlag()
			test.mutate(&flag)
			err := flag.Validate()
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("got %v, want ErrInvalid", err)
			}
		})
	}
}

func TestEvaluateIgnoresNestedContextShape(t *testing.T) {
	flag := boolFlag()
	flag.Rules = []Rule{{Attribute: "organization.id", Equals: "acme", Variant: "on"}}
	context := map[string]any{
		"targetingKey": "user-2",
		"organization": map[string]any{"id": "acme"},
	}
	got, err := evaluate(flag, context)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if got.Variant != "off" || got.Reason != "STATIC" {
		t.Fatalf("nested context must not match the flat wire contract: %#v", got)
	}
}

func TestEvaluateRequiresStringTargetingKey(t *testing.T) {
	for name, targetingKey := range map[string]any{"number": 42, "empty": ""} {
		_, err := evaluate(boolFlag(), map[string]any{"targetingKey": targetingKey})
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s targetingKey: got %v, want ErrInvalid", name, err)
		}
	}
}

func boolFlag() Flag {
	return Flag{
		Key:            "checkout-v2",
		Kind:           Boolean,
		Enabled:        true,
		DefaultVariant: "off",
		Variants: map[string]any{
			"off": false,
			"on":  true,
		},
		Revision: 7,
	}
}
