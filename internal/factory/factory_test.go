package factory

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestValidateRejectsUnsafeSQLiteDeployment(t *testing.T) {
	recipe := DefaultRecipe()
	recipe.Deployment.Target = "kubernetes"
	if err := Validate(recipe); err == nil || !strings.Contains(err.Error(), "requires a shared database") {
		t.Fatalf("expected shared-database compatibility error, got %v", err)
	}
}

func TestValidateRejectsReplicaCountIgnoredByTarget(t *testing.T) {
	recipe := DefaultRecipe()
	recipe.Database.Authority = "postgres"
	recipe.Deployment.Target = "aws-ec2"
	recipe.Deployment.Replicas = 2
	if err := Validate(recipe); err == nil || !strings.Contains(err.Error(), "exactly one replica") {
		t.Fatalf("expected single-instance compatibility error, got %v", err)
	}
}

func TestValidateRejectsMultipleReplicasForNonSharedAuthority(t *testing.T) {
	recipe := DefaultRecipe()
	recipe.Deployment.Replicas = 2
	if err := Validate(recipe); err == nil || !strings.Contains(err.Error(), "sqlite authority requires exactly one replica") {
		t.Fatalf("expected non-shared authority error, got %v", err)
	}
}

func TestGenerateComposesDirectoryAndArchive(t *testing.T) {
	recipe := DefaultRecipe()
	recipe.Name = "customer-flags"
	recipe.Service.Language = "typescript"
	destination := filepath.Join(t.TempDir(), recipe.Name)
	result, err := Generate(recipe, destination)
	if err != nil {
		t.Fatal(err)
	}
	if result.Directory != destination {
		t.Fatalf("directory = %q, want %q", result.Directory, destination)
	}
	if result.Archive != destination+".zip" {
		t.Fatalf("archive = %q, want %q", result.Archive, destination+".zip")
	}
	assertFileContains(t, filepath.Join(destination, "src/store/sqlite.ts"), "class SqliteAuthority")
	assertFileMissing(t, filepath.Join(destination, "src/store/postgres.ts"))
	assertFileContains(t, filepath.Join(destination, "deploy/docker/compose.yaml"), "reaper-data")
	assertFileContains(t, filepath.Join(destination, "REAPER.lock.yaml"), "originRecipeDigest: sha256:")
	assertFileContains(t, filepath.Join(destination, "REAPER.lock.yaml"), "deliveryPack: both/v1")
	assertFileContains(t, filepath.Join(destination, ".agents/skills/onboard-domain/SKILL.md"), "Onboard a customer domain")
	assertFileContains(t, filepath.Join(destination, ".claude/skills/swap-database/SKILL.md"), "Swap the database authority")
	assertPairedGuides(t, destination)
	assertArchiveEntry(t, result.Archive, recipe.Name+"/src/flags/evaluate.ts")
	if _, err := Generate(recipe, destination); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected existing destination error, got %v", err)
	}
}

func TestGenerateZipOnlyLeavesNoDirectory(t *testing.T) {
	recipe := DefaultRecipe()
	recipe.Name = "portable-flags"
	recipe.Service.Language = "python"
	recipe.Delivery.Format = "zip"
	destination := filepath.Join(t.TempDir(), recipe.Name)
	result, err := Generate(recipe, destination)
	if err != nil {
		t.Fatal(err)
	}
	if result.Directory != "" {
		t.Fatalf("unexpected directory result %q", result.Directory)
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("zip-only generation left directory: %v", err)
	}
	assertArchiveEntry(t, result.Archive, recipe.Name+"/reaper_flags/__main__.py")
}

func TestConcurrentGenerationDoesNotDeleteWinningArtifacts(t *testing.T) {
	const rounds = 12
	for round := range rounds {
		goRecipe := DefaultRecipe()
		goRecipe.Name = fmt.Sprintf("concurrent-go-%d", round)
		typeScriptRecipe := DefaultRecipe()
		typeScriptRecipe.Name = fmt.Sprintf("concurrent-typescript-%d", round)
		typeScriptRecipe.Service.Language = "typescript"
		destination := filepath.Join(t.TempDir(), fmt.Sprintf("winner-%d", round))
		start := make(chan struct{})
		results := make(chan error, 2)
		for _, recipe := range []Recipe{goRecipe, typeScriptRecipe} {
			go func(selected Recipe) {
				<-start
				_, err := Generate(selected, destination)
				results <- err
			}(recipe)
		}
		close(start)
		successes := 0
		for range 2 {
			if err := <-results; err == nil {
				successes++
			}
		}
		if successes != 1 {
			t.Fatalf("round %d completed %d generations, want exactly one", round, successes)
		}
		assertPathExists(t, destination)
		assertPathExists(t, destination+".zip")
		generated, err := ReadRecipe(filepath.Join(destination, "REAPER.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		assertArchiveEntry(t, destination+".zip", generated.Name+"/REAPER.yaml")
	}
}

func TestGenerationIsDeterministic(t *testing.T) {
	recipe := DefaultRecipe()
	recipe.Name = "stable-flags"
	recipe.Delivery.Format = "both"
	root := t.TempDir()
	first := filepath.Join(root, "first")
	second := filepath.Join(root, "second")
	firstResult, err := Generate(recipe, first)
	if err != nil {
		t.Fatal(err)
	}
	secondResult, err := Generate(recipe, second)
	if err != nil {
		t.Fatal(err)
	}
	firstFiles := directoryContents(t, first)
	secondFiles := directoryContents(t, second)
	if len(firstFiles) != len(secondFiles) {
		t.Fatalf("generated file counts differ: %d and %d", len(firstFiles), len(secondFiles))
	}
	for path, firstBody := range firstFiles {
		secondBody, ok := secondFiles[path]
		if !ok {
			t.Fatalf("second output missing %s", path)
		}
		if !bytes.Equal(firstBody, secondBody) {
			t.Fatalf("generated file differs: %s", path)
		}
	}
	firstArchive, err := os.ReadFile(firstResult.Archive)
	if err != nil {
		t.Fatal(err)
	}
	secondArchive, err := os.ReadFile(secondResult.Archive)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstArchive, secondArchive) {
		t.Fatal("generated ZIP archives differ")
	}
}

func TestEveryCompatibleCatalogCombinationRenders(t *testing.T) {
	root := t.TempDir()
	for _, language := range ProductCatalog().Languages {
		testLanguageMatrix(t, root, language.Value)
	}
}

func TestKubernetesPackContainsValidResourceDocuments(t *testing.T) {
	recipe := DefaultRecipe()
	recipe.Name = "cluster-flags"
	recipe.Service.Language = "python"
	recipe.Database.Authority = "postgres"
	recipe.Deployment.Target = "kubernetes"
	recipe.Deployment.Replicas = 3
	recipe.Delivery.Format = "directory"
	result, err := Generate(recipe, filepath.Join(t.TempDir(), recipe.Name))
	if err != nil {
		t.Fatal(err)
	}
	kinds := make(map[string]bool)
	paths := make([]string, 0, 8)
	for _, pattern := range []string{"deploy/kubernetes/*.yaml", "deploy/kubernetes/base/*.yaml"} {
		matches, err := filepath.Glob(filepath.Join(result.Directory, pattern))
		if err != nil {
			t.Fatal(err)
		}
		paths = append(paths, matches...)
	}
	for _, path := range paths {
		decodeKubernetesDocuments(t, path, kinds)
	}
	for _, kind := range []string{
		"ConfigMap", "Deployment", "Kustomization", "NetworkPolicy",
		"PodDisruptionBudget", "Secret", "Service", "ServiceAccount",
	} {
		if !kinds[kind] {
			t.Fatalf("Kubernetes output missing %s", kind)
		}
	}
}

func decodeKubernetesDocuments(t *testing.T, path string, kinds map[string]bool) {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(body))
	for {
		var document struct {
			Kind string `yaml:"kind"`
		}
		err := decoder.Decode(&document)
		if errors.Is(err, io.EOF) {
			return
		}
		if err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		if document.Kind != "" {
			kinds[document.Kind] = true
		}
	}
}

func testLanguageMatrix(t *testing.T, root, language string) {
	t.Helper()
	for _, database := range ProductCatalog().Databases {
		for _, deployment := range ProductCatalog().Deployments {
			testCatalogCombination(t, root, language, database.Value, deployment.Value)
		}
	}
}

func testCatalogCombination(t *testing.T, root, language, database, deployment string) {
	t.Helper()
	recipe := DefaultRecipe()
	recipe.Name = language + "-" + database + "-" + deployment
	recipe.Service.Language = language
	recipe.Database.Authority = database
	recipe.Deployment.Target = deployment
	recipe.Delivery.Format = "directory"
	selected, exists := findDeployment(deployment)
	if !exists {
		t.Fatalf("catalog deployment is not registered: %s", deployment)
	}
	if !selected.supportsDatabase(database) {
		if err := Validate(recipe); err == nil {
			t.Fatalf("unsafe combination accepted: %s", recipe.Name)
		}
		return
	}
	recipe.Deployment.Replicas = selected.replicas.Default
	result, err := Generate(recipe, filepath.Join(root, recipe.Name))
	if err != nil {
		t.Fatalf("generate %s: %v", recipe.Name, err)
	}
	assertSelectedLanguage(t, result.Directory, language)
	assertSelectedDatabase(t, result.Directory, language, database)
	if _, err := os.Stat(filepath.Join(result.Directory, "deploy", deployment)); err != nil {
		t.Fatalf("%s deployment output: %v", recipe.Name, err)
	}
}

func assertSelectedLanguage(t *testing.T, root, language string) {
	t.Helper()
	if language == "go" {
		assertFileContains(t, filepath.Join(root, "internal/flags/evaluate.go"), "func Evaluate")
		assertFileMissing(t, filepath.Join(root, "src/flags/evaluate.ts"))
		assertFileMissing(t, filepath.Join(root, "reaper_flags/flags/evaluate.py"))
		return
	}
	if language == "typescript" {
		assertFileContains(t, filepath.Join(root, "src/flags/evaluate.ts"), "function evaluate")
		assertFileMissing(t, filepath.Join(root, "reaper_flags/flags/evaluate.py"))
		return
	}
	assertFileContains(t, filepath.Join(root, "reaper_flags/flags/evaluate.py"), "def evaluate")
	assertFileMissing(t, filepath.Join(root, "src/flags/evaluate.ts"))
}

func assertSelectedDatabase(t *testing.T, root, language, database string) {
	t.Helper()
	if language == "go" {
		assertFileContains(
			t,
			filepath.Join(root, "internal/store", database, database+".go"),
			"Authority",
		)
		other := "sqlite"
		if database == "sqlite" {
			other = "postgres"
		}
		assertFileMissing(t, filepath.Join(root, "internal/store", other, other+".go"))
		return
	}
	extension := ".ts"
	storeRoot := filepath.Join(root, "src/store")
	if language == "python" {
		extension = ".py"
		storeRoot = filepath.Join(root, "reaper_flags/store")
	}
	assertFileContains(t, filepath.Join(storeRoot, database+extension), "Authority")
	other := "sqlite"
	if database == "sqlite" {
		other = "postgres"
	}
	assertFileMissing(t, filepath.Join(storeRoot, other+extension))
}

func assertFileContains(t *testing.T, path, fragment string) {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(body, []byte(fragment)) {
		t.Fatalf("%s does not contain %q", path, fragment)
	}
}

func assertFileMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("unexpected generated file %s", path)
	}
}

func assertPairedGuides(t *testing.T, root string) {
	t.Helper()
	agents, err := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	claude, err := os.ReadFile(filepath.Join(root, "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(agents, claude) {
		t.Fatal("generated AGENTS.md and CLAUDE.md differ")
	}
}

func assertArchiveEntry(t *testing.T, path, wanted string) {
	t.Helper()
	archive, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	for _, file := range archive.File {
		if file.Name == wanted {
			return
		}
	}
	t.Fatalf("archive %s does not contain %s", path, wanted)
}

func directoryContents(t *testing.T, root string) map[string][]byte {
	t.Helper()
	files := make(map[string][]byte)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[relative] = body
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}
