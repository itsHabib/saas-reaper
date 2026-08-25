// Command go-example evaluates a reaped flag through OpenFeature and OFREP.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	ofrep "github.com/open-feature/go-sdk-contrib/providers/ofrep"
	"github.com/open-feature/go-sdk/openfeature"
)

type result struct {
	Language string `json:"language"`
	Value    bool   `json:"value"`
	Variant  string `json:"variant"`
	Reason   string `json:"reason"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	endpoint := environment("OFREP_ENDPOINT", "http://127.0.0.1:8080/environments/production")
	token := os.Getenv("REAPER_EVALUATION_TOKEN")
	if token == "" {
		return errors.New("REAPER_EVALUATION_TOKEN is required")
	}
	provider := ofrep.NewProvider(endpoint, ofrep.WithBearerToken(token))
	if err := openfeature.SetProviderAndWait(provider); err != nil {
		return fmt.Errorf("set OFREP provider: %w", err)
	}
	defer openfeature.Shutdown()
	client := openfeature.NewClient("reaper-go-example")
	evaluationContext := openfeature.NewEvaluationContext(
		environment("TARGETING_KEY", "user-2"),
		map[string]any{"organization.id": environment("ORGANIZATION_ID", "acme")},
	)
	details, err := client.BooleanValueDetails(
		context.Background(),
		"checkout-v2",
		false,
		evaluationContext,
	)
	if err != nil {
		return fmt.Errorf("evaluate checkout-v2: %w", err)
	}
	return json.NewEncoder(os.Stdout).Encode(result{
		Language: "go",
		Value:    details.Value,
		Variant:  details.Variant,
		Reason:   string(details.Reason),
	})
}

func environment(name, fallback string) string {
	value := os.Getenv(name)
	if value != "" {
		return value
	}
	return fallback
}
