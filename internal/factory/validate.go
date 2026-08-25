package factory

import (
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
		return fmt.Errorf("domain tenant is required")
	}
	return validateAttributes(recipe.Domain.TargetingAttributes)
}

func validateIdentity(recipe Recipe) error {
	if recipe.Schema != Schema {
		return fmt.Errorf("schema must be %q", Schema)
	}
	if !projectName.MatchString(recipe.Name) {
		return fmt.Errorf("name must be a lowercase DNS label between 2 and 63 characters")
	}
	if recipe.Capability != "feature-flags" {
		return fmt.Errorf("capability must be feature-flags")
	}
	return nil
}

func validateSelections(recipe Recipe) error {
	if !hasChoice(ProductCatalog().Languages, recipe.Service.Language) {
		return fmt.Errorf("unsupported service language %q", recipe.Service.Language)
	}
	if !hasChoice(ProductCatalog().Databases, recipe.Database.Authority) {
		return fmt.Errorf("unsupported database authority %q", recipe.Database.Authority)
	}
	if !hasChoice(ProductCatalog().Deployments, recipe.Deployment.Target) {
		return fmt.Errorf("unsupported deployment target %q", recipe.Deployment.Target)
	}
	if !hasChoice(ProductCatalog().Deliveries, recipe.Delivery.Format) {
		return fmt.Errorf("unsupported delivery format %q", recipe.Delivery.Format)
	}
	return nil
}

func validateDeployment(recipe Recipe) error {
	if recipe.Deployment.Replicas < 1 {
		return fmt.Errorf("deployment replicas must be at least one")
	}
	if isSingleInstanceTarget(recipe.Deployment.Target) && recipe.Deployment.Replicas != 1 {
		return fmt.Errorf("%s requires exactly one replica", recipe.Deployment.Target)
	}
	if recipe.Database.Authority == "sqlite" && recipe.Deployment.Replicas != 1 {
		return fmt.Errorf("sqlite requires exactly one replica")
	}
	if recipe.Database.Authority == "sqlite" && requiresExternalDatabase(recipe.Deployment.Target) {
		return fmt.Errorf("%s requires postgres; sqlite is not a durable multi-host authority", recipe.Deployment.Target)
	}
	return nil
}

func hasChoice(choices []Choice, value string) bool {
	for _, choice := range choices {
		if choice.Value == value {
			return true
		}
	}
	return false
}

func requiresExternalDatabase(target string) bool {
	return target == "aws-ecs" || target == "gcp-cloud-run" || target == "kubernetes"
}

func isSingleInstanceTarget(target string) bool {
	return target == "docker" || target == "aws-ec2"
}

func validateAttributes(attributes []string) error {
	if len(attributes) == 0 {
		return fmt.Errorf("at least one targeting attribute is required")
	}
	seen := make(map[string]struct{}, len(attributes))
	for _, attribute := range attributes {
		if strings.TrimSpace(attribute) == "" {
			return fmt.Errorf("targeting attributes cannot be empty")
		}
		if _, ok := seen[attribute]; ok {
			return fmt.Errorf("duplicate targeting attribute %q", attribute)
		}
		seen[attribute] = struct{}{}
	}
	if _, ok := seen["targetingKey"]; !ok {
		return fmt.Errorf("targeting attributes must include targetingKey")
	}
	return nil
}
