package flags

import "fmt"

var allowedAttributes = map[string]struct{}{
	"organization.id": {},
	"targetingKey":    {},
}

func validateAttribute(attribute string) error {
	if _, ok := allowedAttributes[attribute]; ok {
		return nil
	}
	return fmt.Errorf("%w: targeting attribute %q is not allowed by the domain", ErrInvalid, attribute)
}

func validateRule(flag Flag, rule Rule) error {
	if err := validateAttribute(rule.Attribute); err != nil {
		return err
	}
	if rule.Equals == "" {
		return fmt.Errorf("%w: equals value is required", ErrInvalid)
	}
	if _, ok := flag.Variants[rule.Variant]; !ok {
		return fmt.Errorf("%w: variant %q is not defined", ErrInvalid, rule.Variant)
	}
	return nil
}

func validateRollout(flag Flag, rollout Rollout) error {
	if err := validateAttribute(rollout.Attribute); err != nil {
		return err
	}
	if rollout.Percentage < 0 || rollout.Percentage > 100 {
		return fmt.Errorf("%w: rollout percentage must be between 0 and 100", ErrInvalid)
	}
	if _, ok := flag.Variants[rollout.Variant]; !ok {
		return fmt.Errorf("%w: rollout variant %q is not defined", ErrInvalid, rollout.Variant)
	}
	return nil
}
