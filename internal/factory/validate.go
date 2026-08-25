package factory

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var projectName = regexp.MustCompile(`^[a-z][a-z0-9-]{1,62}$`)

// Validate rejects unsupported or operationally unsafe combinations.
func Validate(recipe Recipe) error {
	if err := validateIdentity(recipe); err != nil {
		return err
	}
	if err := validateSelections(recipe); err != nil {
		return err
	}
	if err := validateDeployment(recipe); err != nil {
		return err
	}
	if strings.TrimSpace(recipe.Domain.Tenant) == "" {
		return errors.New("domain tenant is required")
	}
	return validateAttributes(recipe.Domain.TargetingAttributes)
}

func validateIdentity(recipe Recipe) error {
	if recipe.Schema != Schema {
		return fmt.Errorf("schema must be %q", Schema)
	}
	if !projectName.MatchString(recipe.Name) {
		return errors.New("name must be a lowercase DNS label between 2 and 63 characters")
	}
	if recipe.Capability != "feature-flags" {
		return errors.New("capability must be feature-flags")
	}
	return nil
}

func validateSelections(recipe Recipe) error {
	if _, exists := findLanguage(recipe.Service.Language); !exists {
		return fmt.Errorf("unsupported service language %q", recipe.Service.Language)
	}
	if _, exists := findDatabase(recipe.Database.Authority); !exists {
		return fmt.Errorf("unsupported database authority %q", recipe.Database.Authority)
	}
	if _, exists := findDeployment(recipe.Deployment.Target); !exists {
		return fmt.Errorf("unsupported deployment target %q", recipe.Deployment.Target)
	}
	if _, exists := findDelivery(recipe.Delivery.Format); !exists {
		return fmt.Errorf("unsupported delivery format %q", recipe.Delivery.Format)
	}
	return nil
}

func validateDeployment(recipe Recipe) error {
	if recipe.Deployment.Replicas < 1 {
		return errors.New("deployment replicas must be at least one")
	}
	database, _ := findDatabase(recipe.Database.Authority)
	deployment, _ := findDeployment(recipe.Deployment.Target)
	if !deployment.supportsDatabase(recipe.Database.Authority) {
		if deployment.requiresShared && !database.shared {
			return fmt.Errorf(
				"%s requires a shared database authority; %s is not durable across hosts",
				recipe.Deployment.Target,
				recipe.Database.Authority,
			)
		}
		return fmt.Errorf(
			"%s does not support database authority %s",
			recipe.Deployment.Target,
			recipe.Database.Authority,
		)
	}
	if !database.shared && recipe.Deployment.Replicas != 1 {
		return fmt.Errorf(
			"%s authority requires exactly one replica; it is not durable across hosts",
			recipe.Database.Authority,
		)
	}
	if deployment.replicas.Maximum == 1 && recipe.Deployment.Replicas != 1 {
		return fmt.Errorf("%s requires exactly one replica", recipe.Deployment.Target)
	}
	if deployment.replicas.Maximum > 1 && recipe.Deployment.Replicas > deployment.replicas.Maximum {
		return fmt.Errorf("%s accepts at most %d replicas", recipe.Deployment.Target, deployment.replicas.Maximum)
	}
	return nil
}

func validateAttributes(attributes []string) error {
	if len(attributes) == 0 {
		return errors.New("at least one targeting attribute is required")
	}
	seen := make(map[string]struct{}, len(attributes))
	for _, attribute := range attributes {
		if strings.TrimSpace(attribute) == "" {
			return errors.New("targeting attributes cannot be empty")
		}
		if _, ok := seen[attribute]; ok {
			return fmt.Errorf("duplicate targeting attribute %q", attribute)
		}
		seen[attribute] = struct{}{}
	}
	if _, ok := seen["targetingKey"]; !ok {
		return errors.New("targeting attributes must include targetingKey")
	}
	return nil
}
