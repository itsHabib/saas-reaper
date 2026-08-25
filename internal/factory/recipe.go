// Package factory composes customer choices into an owned feature-flag repository.
package factory

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Schema is the accepted customer recipe schema version.
const Schema = "reaper.dev/v0alpha2"

// Recipe is the customer-authored source for one generated repository.
type Recipe struct {
	Schema     string     `json:"schema" yaml:"schema"`
	Name       string     `json:"name" yaml:"name"`
	Capability string     `json:"capability" yaml:"capability"`
	Service    Runtime    `json:"service" yaml:"service"`
	Database   Database   `json:"database" yaml:"database"`
	Deployment Deployment `json:"deployment" yaml:"deployment"`
	Delivery   Delivery   `json:"delivery" yaml:"delivery"`
	Domain     Domain     `json:"domain" yaml:"domain"`
}

// Runtime selects the generated backend implementation.
type Runtime struct {
	Language string `json:"language" yaml:"language"`
}

// Database selects the durable authority used by management writes.
type Database struct {
	Authority string `json:"authority" yaml:"authority"`
}

// Deployment selects the infrastructure pack and replica intent.
type Deployment struct {
	Target   string `json:"target" yaml:"target"`
	Replicas int    `json:"replicas" yaml:"replicas"`
}

// Delivery selects how the generated repository is handed to the customer.
type Delivery struct {
	Format string `json:"format" yaml:"format"`
}

// Domain records the initial customer vocabulary and targeting boundary.
type Domain struct {
	Tenant              string   `json:"tenant" yaml:"tenant"`
	TargetingAttributes []string `json:"targetingAttributes" yaml:"targetingAttributes"`
}

// ReadRecipe decodes a strict customer recipe.
func ReadRecipe(path string) (Recipe, error) {
	data, err := os.ReadFile(path) //nolint:gosec // The CLI caller explicitly selects the local recipe path.
	if err != nil {
		return Recipe{}, fmt.Errorf("read recipe: %w", err)
	}
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	decoder.KnownFields(true)
	var recipe Recipe
	if err := decoder.Decode(&recipe); err != nil {
		return Recipe{}, fmt.Errorf("decode recipe: %w", err)
	}
	return recipe, nil
}

// EncodeRecipe returns the normalized recipe stored in a generated repository.
func EncodeRecipe(recipe Recipe) ([]byte, error) {
	data, err := yaml.Marshal(recipe)
	if err != nil {
		return nil, fmt.Errorf("encode recipe: %w", err)
	}
	return data, nil
}

// DefaultRecipe gives the interactive configurator a useful starting point.
func DefaultRecipe() Recipe {
	return Recipe{
		Schema:     Schema,
		Name:       "reaper-flags",
		Capability: "feature-flags",
		Service:    Runtime{Language: "go"},
		Database:   Database{Authority: "sqlite"},
		Deployment: Deployment{Target: "docker", Replicas: 1},
		Delivery:   Delivery{Format: "both"},
		Domain: Domain{
			Tenant:              "organization",
			TargetingAttributes: []string{"targetingKey", "organization.id"},
		},
	}
}
