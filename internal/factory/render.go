package factory

import (
	"crypto/sha256"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

// FactoryVersion identifies the template set recorded in generated lock receipts.
const FactoryVersion = "0.1.0"

//go:embed templates
var templateFiles embed.FS

// Result identifies every artifact handed to the customer.
type Result struct {
	Directory string
	Archive   string
}

type renderData struct {
	Recipe         Recipe
	FactoryVersion string
	RecipeDigest   string
	Attributes     string
	ModuleName     string
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
	if err := ensureAvailable(absolute, delivery); err != nil {
		return "", "", err
	}
	parent := filepath.Dir(absolute)
	if err := os.MkdirAll(parent, 0o755); err != nil {
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
	if err := os.WriteFile(filepath.Join(staging, "REAPER.yaml"), recipeData, 0o644); err != nil {
		return fmt.Errorf("write normalized recipe: %w", err)
	}
	return nil
}

func publishRepository(staging, destination string, recipe Recipe) (Result, error) {
	result := Result{}
	if recipe.Delivery.Format == "directory" || recipe.Delivery.Format == "both" {
		if err := os.Rename(staging, destination); err != nil {
			return Result{}, fmt.Errorf("publish directory: %w", err)
		}
		result.Directory = destination
		staging = destination
	}
	if recipe.Delivery.Format == "zip" || recipe.Delivery.Format == "both" {
		archive := destination + ".zip"
		if err := writeArchive(staging, archive, recipe.Name); err != nil {
			return Result{}, err
		}
		result.Archive = archive
	}
	return result, nil
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
	return renderData{
		Recipe:         recipe,
		FactoryVersion: FactoryVersion,
		RecipeDigest:   fmt.Sprintf("sha256:%x", sha256.Sum256(recipeData)),
		Attributes:     strings.Join(quoted, ", "),
		ModuleName:     strings.ReplaceAll(recipe.Name, "-", "_"),
	}, nil
}

func ensureAvailable(destination, delivery string) error {
	if _, err := os.Stat(destination); err == nil {
		return fmt.Errorf("destination already exists: %s", destination)
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		return fmt.Errorf("inspect destination: %w", err)
	}
	if delivery != "zip" && delivery != "both" {
		return nil
	}
	archive := destination + ".zip"
	if _, err := os.Stat(archive); err == nil {
		return fmt.Errorf("archive already exists: %s", archive)
	}
	if _, err := os.Stat(archive); !os.IsNotExist(err) {
		return fmt.Errorf("inspect archive: %w", err)
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
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return fmt.Errorf("create output parent: %w", err)
	}
	file, err := os.Create(destination)
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
			if err := os.MkdirAll(directory, 0o755); err != nil {
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
