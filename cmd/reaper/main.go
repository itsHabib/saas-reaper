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
		fmt.Fprintln(os.Stderr, "reaper:", err)
		os.Exit(1)
	}
}

func run(arguments []string, input io.Reader, output io.Writer) error {
	if len(arguments) == 0 {
		printUsage(output)
		return nil
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
		printUsage(output)
		return nil
	default:
		return fmt.Errorf("unknown command %q", arguments[0])
	}
}

func printUsage(output io.Writer) {
	fmt.Fprintln(output, "SaaS Reaper composes a customer-owned SaaS capability repository.")
	fmt.Fprintln(output)
	fmt.Fprintln(output, "Usage:")
	fmt.Fprintln(output, "  reaper new [--out PATH]                 interactive configurator")
	fmt.Fprintln(output, "  reaper generate --recipe FILE --out PATH")
	fmt.Fprintln(output, "  reaper catalog                          machine-readable choices")
	fmt.Fprintln(output, "  reaper serve [--addr 127.0.0.1:8090]   browser configurator")
}

func serve(arguments []string, output io.Writer) error {
	command := flag.NewFlagSet("serve", flag.ContinueOnError)
	command.SetOutput(output)
	address := command.String("addr", "127.0.0.1:8090", "configurator listen address")
	if err := command.Parse(arguments); err != nil {
		return err
	}
	fmt.Fprintf(output, "SaaS Reaper configurator: http://%s\n", *address)
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
	printResult(output, result)
	return nil
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
	fmt.Fprintln(output, "SaaS Reaper — compose your feature-flag service")
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
	deployments := compatibleDeployments(recipe.Database.Authority)
	recipe.Deployment.Target, err = askChoice(reader, output, "Deployment target", deployments, deployments[0].Value)
	if err != nil {
		return err
	}
	recipe.Deployment.Replicas, err = askNumber(reader, output, "Replicas", defaultReplicas(recipe.Deployment.Target))
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
	printResult(output, result)
	return nil
}

func compatibleDeployments(database string) []factory.Choice {
	all := factory.ProductCatalog().Deployments
	if database == "postgres" {
		return all
	}
	compatible := make([]factory.Choice, 0, 2)
	for _, choice := range all {
		if choice.Value != "docker" && choice.Value != "aws-ec2" {
			continue
		}
		compatible = append(compatible, choice)
	}
	return compatible
}

func defaultReplicas(deployment string) int {
	if deployment == "aws-ecs" || deployment == "gcp-cloud-run" || deployment == "kubernetes" {
		return 2
	}
	return 1
}

func askText(reader *bufio.Reader, output io.Writer, label, fallback string) (string, error) {
	fmt.Fprintf(output, "%s [%s]: ", label, fallback)
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
	fmt.Fprintln(output, label+":")
	defaultIndex := 0
	for index, choice := range choices {
		fmt.Fprintf(output, "  %d) %s — %s\n", index+1, choice.Label, choice.Description)
		if choice.Value == fallback {
			defaultIndex = index
		}
	}
	for {
		fmt.Fprintf(output, "Choose [%d]: ", defaultIndex+1)
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
		fmt.Fprintln(output, "Enter one of the numbered choices.")
	}
}

func askNumber(reader *bufio.Reader, output io.Writer, label string, fallback int) (int, error) {
	for {
		fmt.Fprintf(output, "%s [%d]: ", label, fallback)
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
		fmt.Fprintln(output, "Enter a positive whole number.")
	}
}

func printResult(output io.Writer, result factory.Result) {
	if result.Directory != "" {
		fmt.Fprintln(output, "Repository:", result.Directory)
	}
	if result.Archive != "" {
		fmt.Fprintln(output, "ZIP archive:", result.Archive)
	}
}
