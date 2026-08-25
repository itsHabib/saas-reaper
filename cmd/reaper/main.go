// Command reaper composes and delivers customer-owned SaaS capability repositories.
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/itsHabib/saas-reaper-poc/internal/configurator"
	"github.com/itsHabib/saas-reaper-poc/internal/factory"
)

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "reaper:", err)
		os.Exit(1)
	}
}

func run(arguments []string, input io.Reader, output io.Writer) error {
	if len(arguments) == 0 {
		return printUsage(output)
	}
	switch arguments[0] {
	case "catalog":
		return printCatalog(output)
	case "generate":
		return generate(arguments[1:], output)
	case "new":
		return configure(arguments[1:], input, output)
	case "serve":
		return serve(arguments[1:], output)
	case "help", "-h", "--help":
		return printUsage(output)
	default:
		return fmt.Errorf("unknown command %q", arguments[0])
	}
}

func printUsage(output io.Writer) error {
	const usage = `SaaS Reaper composes a customer-owned SaaS capability repository.

Usage:
  reaper new [--out PATH]                 interactive configurator
  reaper generate --recipe FILE --out PATH
  reaper catalog                          machine-readable choices
  reaper serve [--addr 127.0.0.1:8090]   browser configurator
`
	return writeOutputf(output, usage)
}

func serve(arguments []string, output io.Writer) error {
	command := flag.NewFlagSet("serve", flag.ContinueOnError)
	command.SetOutput(output)
	address := command.String("addr", "127.0.0.1:8090", "configurator listen address")
	if err := command.Parse(arguments); err != nil {
		return err
	}
	if err := writeOutputf(output, "SaaS Reaper configurator: http://%s\n", *address); err != nil {
		return err
	}
	return configurator.Serve(*address)
}

func printCatalog(output io.Writer) error {
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(factory.ProductCatalog()); err != nil {
		return fmt.Errorf("encode catalog: %w", err)
	}
	return nil
}

func generate(arguments []string, output io.Writer) error {
	command := flag.NewFlagSet("generate", flag.ContinueOnError)
	command.SetOutput(output)
	recipePath := command.String("recipe", "", "path to the Reaper recipe")
	destination := command.String("out", "", "generated repository destination")
	if err := command.Parse(arguments); err != nil {
		return err
	}
	if *recipePath == "" {
		return errors.New("--recipe is required")
	}
	if *destination == "" {
		return errors.New("--out is required")
	}
	recipe, err := factory.ReadRecipe(*recipePath)
	if err != nil {
		return err
	}
	result, err := factory.Generate(recipe, *destination)
	if err != nil {
		return err
	}
	return printResult(output, result)
}

func configure(arguments []string, input io.Reader, output io.Writer) error {
	command := flag.NewFlagSet("new", flag.ContinueOnError)
	command.SetOutput(output)
	destination := command.String("out", "", "generated repository destination")
	if err := command.Parse(arguments); err != nil {
		return err
	}
	reader := bufio.NewReader(input)
	recipe := factory.DefaultRecipe()
	if err := writeOutputf(output, "SaaS Reaper — compose your feature-flag service\n"); err != nil {
		return err
	}
	name, err := askText(reader, output, "Project name", recipe.Name)
	if err != nil {
		return err
	}
	recipe.Name = name
	recipe.Service.Language, err = askChoice(reader, output, "Service language", factory.ProductCatalog().Languages, recipe.Service.Language)
	if err != nil {
		return err
	}
	recipe.Database.Authority, err = askChoice(reader, output, "Database authority", factory.ProductCatalog().Databases, recipe.Database.Authority)
	if err != nil {
		return err
	}
	deployments := factory.CompatibleDeployments(recipe.Database.Authority)
	recipe.Deployment.Target, err = askChoice(reader, output, "Deployment target", deployments, deployments[0].Value)
	if err != nil {
		return err
	}
	recipe.Deployment.Replicas, err = askNumber(
		reader,
		output,
		"Replicas",
		factory.DefaultReplicas(recipe.Deployment.Target),
	)
	if err != nil {
		return err
	}
	recipe.Delivery.Format, err = askChoice(reader, output, "Delivery", factory.ProductCatalog().Deliveries, recipe.Delivery.Format)
	if err != nil {
		return err
	}
	selectedDestination := *destination
	if selectedDestination == "" {
		selectedDestination = filepath.Join(".", recipe.Name)
	}
	result, err := factory.Generate(recipe, selectedDestination)
	if err != nil {
		return err
	}
	return printResult(output, result)
}

func askText(reader *bufio.Reader, output io.Writer, label, fallback string) (string, error) {
	if err := writeOutputf(output, "%s [%s]: ", label, fallback); err != nil {
		return "", err
	}
	value, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback, nil
	}
	return value, nil
}

func askChoice(reader *bufio.Reader, output io.Writer, label string, choices []factory.Choice, fallback string) (string, error) {
	if err := writeOutputf(output, "%s:\n", label); err != nil {
		return "", err
	}
	defaultIndex := 0
	for index, choice := range choices {
		if err := writeOutputf(output, "  %d) %s — %s\n", index+1, choice.Label, choice.Description); err != nil {
			return "", err
		}
		if choice.Value == fallback {
			defaultIndex = index
		}
	}
	for {
		if err := writeOutputf(output, "Choose [%d]: ", defaultIndex+1); err != nil {
			return "", err
		}
		value, err := reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return "", err
		}
		value = strings.TrimSpace(value)
		if value == "" {
			return choices[defaultIndex].Value, nil
		}
		selected, conversionErr := strconv.Atoi(value)
		if conversionErr == nil && selected > 0 && selected <= len(choices) {
			return choices[selected-1].Value, nil
		}
		if err := writeOutputf(output, "Enter one of the numbered choices.\n"); err != nil {
			return "", err
		}
	}
}

func askNumber(reader *bufio.Reader, output io.Writer, label string, fallback int) (int, error) {
	for {
		if err := writeOutputf(output, "%s [%d]: ", label, fallback); err != nil {
			return 0, err
		}
		value, err := reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return 0, err
		}
		value = strings.TrimSpace(value)
		if value == "" {
			return fallback, nil
		}
		selected, conversionErr := strconv.Atoi(value)
		if conversionErr == nil && selected > 0 {
			return selected, nil
		}
		if err := writeOutputf(output, "Enter a positive whole number.\n"); err != nil {
			return 0, err
		}
	}
}

func printResult(output io.Writer, result factory.Result) error {
	if result.Directory != "" {
		if err := writeOutputf(output, "Repository: %s\n", result.Directory); err != nil {
			return err
		}
	}
	if result.Archive != "" {
		if err := writeOutputf(output, "Archive: %s\n", result.Archive); err != nil {
			return err
		}
	}
	return nil
}

func writeOutputf(output io.Writer, format string, arguments ...any) error {
	if _, err := fmt.Fprintf(output, format, arguments...); err != nil {
		return fmt.Errorf("write output: %w", err)
	}
	return nil
}
