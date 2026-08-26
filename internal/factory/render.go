package factory

import (
	"crypto/sha256"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

// FactoryVersion identifies the template set recorded in generated lock receipts.
const FactoryVersion = "0.4.0"

//go:embed templates
var templateFiles embed.FS

// Result identifies every artifact handed to the customer.
type Result struct {
	Directory string
	Archive   string
}

type renderData struct {
	Recipe            Recipe
	FactoryVersion    string
	RecipeDigest      string
	Attributes        string
	ModuleName        string
	PolicyPath        string
	ProofPath         string
	LanguageVersion   string
	DatabaseVersion   string
	DeploymentVersion string
	DeliveryVersion   string
}

// Generate validates and atomically renders one customer-owned repository.
func Generate(recipe Recipe, destination string) (Result, error) {
	if err := Validate(recipe); err != nil {
		return Result{}, err
	}
	absolute, staging, err := prepareDestination(destination, recipe.Delivery.Format)
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(staging)
	if err := renderRepository(staging, recipe); err != nil {
		return Result{}, err
	}
	return publishRepository(staging, absolute, recipe)
}

func prepareDestination(destination, delivery string) (string, string, error) {
	absolute, err := filepath.Abs(destination)
	if err != nil {
		return "", "", fmt.Errorf("resolve destination: %w", err)
	}
	registered, exists := findDelivery(delivery)
	if !exists {
		return "", "", fmt.Errorf("delivery format %q is not registered", delivery)
	}
	if err := ensureAvailable(registered.artifacts.paths(absolute)); err != nil {
		return "", "", err
	}
	parent := filepath.Dir(absolute)
	if err := os.MkdirAll(parent, 0o755); err != nil { //nolint:gosec // Generated repositories are customer-readable source.
		return "", "", fmt.Errorf("create destination parent: %w", err)
	}
	staging, err := os.MkdirTemp(parent, ".reaper-generate-")
	if err != nil {
		return "", "", fmt.Errorf("create staging directory: %w", err)
	}
	return absolute, staging, nil
}

func renderRepository(staging string, recipe Recipe) error {
	data, err := newRenderData(recipe)
	if err != nil {
		return err
	}
	layers := []string{
		"templates/common",
		"templates/languages/" + recipe.Service.Language + "/base",
		"templates/languages/" + recipe.Service.Language + "/" + recipe.Database.Authority,
		"templates/deployments/" + recipe.Deployment.Target,
	}
	for _, layer := range layers {
		if err := renderLayer(staging, layer, data); err != nil {
			return err
		}
	}
	if err := projectSkills(staging); err != nil {
		return err
	}
	recipeData, err := EncodeRecipe(recipe)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(staging, "REAPER.yaml"), recipeData, 0o644); err != nil { //nolint:gosec // The recipe is customer-readable source, not a secret.
		return fmt.Errorf("write normalized recipe: %w", err)
	}
	return nil
}

func publishRepository(staging, destination string, recipe Recipe) (Result, error) {
	delivery, exists := findDelivery(recipe.Delivery.Format)
	if !exists || delivery.publish == nil {
		return Result{}, fmt.Errorf("delivery format %q has no publisher", recipe.Delivery.Format)
	}
	return delivery.publish(staging, destination, recipe.Name)
}

func newRenderData(recipe Recipe) (renderData, error) {
	recipeData, err := EncodeRecipe(recipe)
	if err != nil {
		return renderData{}, err
	}
	quoted := make([]string, 0, len(recipe.Domain.TargetingAttributes))
	for _, attribute := range recipe.Domain.TargetingAttributes {
		quoted = append(quoted, fmt.Sprintf("%q", attribute))
	}
	language, _ := findLanguage(recipe.Service.Language)
	database, _ := findDatabase(recipe.Database.Authority)
	deployment, _ := findDeployment(recipe.Deployment.Target)
	delivery, _ := findDelivery(recipe.Delivery.Format)
	return renderData{
		Recipe:            recipe,
		FactoryVersion:    FactoryVersion,
		RecipeDigest:      fmt.Sprintf("sha256:%x", sha256.Sum256(recipeData)),
		Attributes:        strings.Join(quoted, ", "),
		ModuleName:        strings.ReplaceAll(recipe.Name, "-", "_"),
		PolicyPath:        policyPaths[recipe.Service.Language],
		ProofPath:         proofPaths[recipe.Service.Language],
		LanguageVersion:   language.version,
		DatabaseVersion:   database.version,
		DeploymentVersion: deployment.version,
		DeliveryVersion:   delivery.version,
	}, nil
}

var policyPaths = map[string]string{
	"go":         "internal/flags/",
	"typescript": "src/flags/",
	"python":     "reaper_flags/flags/",
}

var proofPaths = map[string]string{
	"go":         "internal/flags/evaluate_test.go",
	"typescript": "src/flags/evaluate.test.ts",
	"python":     "tests/test_evaluate.py",
}

func publishDirectory(staging, destination, _ string) (Result, error) {
	if err := os.Rename(staging, destination); err != nil {
		return Result{}, fmt.Errorf("publish directory: %w", err)
	}
	return Result{Directory: destination}, nil
}

func publishZIP(staging, destination, rootName string) (Result, error) {
	archive := destination + ".zip"
	if err := writeArchive(staging, archive, rootName); err != nil {
		return Result{}, err
	}
	return Result{Archive: archive}, nil
}

func publishDirectoryAndZIP(staging, destination, rootName string) (Result, error) {
	archive := destination + ".zip"
	if err := writeArchive(staging, archive, rootName); err != nil {
		return Result{}, err
	}
	if err := os.Rename(staging, destination); err != nil {
		publishErr := fmt.Errorf("publish directory: %w", err)
		if cleanupErr := os.Remove(archive); cleanupErr != nil {
			return Result{}, errors.Join(publishErr, fmt.Errorf("remove incomplete archive: %w", cleanupErr))
		}
		return Result{}, publishErr
	}
	return Result{Directory: destination, Archive: archive}, nil
}

func (artifacts deliveryArtifacts) paths(destination string) []string {
	paths := make([]string, 0, 2)
	if artifacts.directory {
		paths = append(paths, destination)
	}
	if artifacts.archiveSuffix != "" {
		paths = append(paths, destination+artifacts.archiveSuffix)
	}
	return paths
}

func ensureAvailable(paths []string) error {
	for _, path := range paths {
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("output already exists: %s", path)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			return fmt.Errorf("inspect output %s: %w", path, err)
		}
	}
	return nil
}

func renderLayer(destination, layer string, data renderData) error {
	return fs.WalkDir(templateFiles, layer, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		relative := strings.TrimPrefix(path, layer+"/")
		if relative == "gitignore.tmpl" {
			relative = ".gitignore.tmpl"
		}
		relative = strings.ReplaceAll(relative, "/init.py.tmpl", "/__init__.py.tmpl")
		relative = strings.ReplaceAll(relative, "/main.py.tmpl", "/__main__.py.tmpl")
		output := filepath.Join(destination, strings.TrimSuffix(relative, ".tmpl"))
		return renderFile(path, output, data)
	})
}

func renderFile(source, destination string, data renderData) error {
	body, err := templateFiles.ReadFile(source)
	if err != nil {
		return fmt.Errorf("read template %s: %w", source, err)
	}
	parsed, err := template.New(source).Option("missingkey=error").Parse(string(body))
	if err != nil {
		return fmt.Errorf("parse template %s: %w", source, err)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil { //nolint:gosec // Generated source directories must be customer-readable.
		return fmt.Errorf("create output parent: %w", err)
	}
	_, statErr := os.Lstat(destination)
	if statErr == nil {
		return fmt.Errorf("template output collision at %s", destination)
	}
	if !os.IsNotExist(statErr) {
		return fmt.Errorf("inspect output %s: %w", destination, statErr)
	}
	file, err := os.Create(destination) //nolint:gosec // The embedded template determines the path beneath staging.
	if err != nil {
		return fmt.Errorf("create output %s: %w", destination, err)
	}
	executeErr := parsed.Execute(file, data)
	closeErr := file.Close()
	if executeErr != nil {
		return fmt.Errorf("render output %s: %w", destination, executeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close output %s: %w", destination, closeErr)
	}
	return nil
}

func projectSkills(root string) error {
	entries, err := os.ReadDir(filepath.Join(root, "skills"))
	if err != nil {
		return fmt.Errorf("read generated skills: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		for _, platform := range []string{".agents", ".claude"} {
			directory := filepath.Join(root, platform, "skills", entry.Name())
			if err := os.MkdirAll(directory, 0o755); err != nil { //nolint:gosec // Skill projections are customer-readable source.
				return fmt.Errorf("create %s skill projection: %w", platform, err)
			}
			target := filepath.Join("..", "..", "..", "skills", entry.Name(), "SKILL.md")
			if err := os.Symlink(target, filepath.Join(directory, "SKILL.md")); err != nil {
				return fmt.Errorf("link %s skill projection: %w", platform, err)
			}
		}
	}
	return nil
}
