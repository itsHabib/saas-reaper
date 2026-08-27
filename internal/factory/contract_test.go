package factory

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

var (
	optionValue           = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	packVersion           = regexp.MustCompile(`^v[1-9][0-9]*$`)
	languageHookTemplates = []string{
		"scripts/setup-language.sh.tmpl",
		"scripts/check-language.sh.tmpl",
		"scripts/start-language.sh.tmpl",
	}
)

func TestCatalogChoicesAreCompleteAndPathSafe(t *testing.T) {
	catalog := ProductCatalog()
	if catalog.Schema != CatalogSchema {
		t.Fatalf("catalog schema = %q, want %q", catalog.Schema, CatalogSchema)
	}
	groups := []struct {
		name    string
		choices []Choice
	}{
		{name: "language", choices: catalog.Languages},
		{name: "database", choices: catalog.Databases},
		{name: "deployment", choices: catalog.Deployments},
		{name: "delivery", choices: catalog.Deliveries},
	}
	for _, group := range groups {
		t.Run(group.name, func(t *testing.T) {
			assertChoices(t, group.choices)
		})
	}
}

func TestRootManifestCatalogMatchesRegistrations(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "REAPER.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Factory struct {
			Languages   []string `yaml:"languages"`
			Databases   []string `yaml:"databases"`
			Deployments []string `yaml:"deployments"`
			Deliveries  []string `yaml:"deliveries"`
		} `yaml:"factory"`
	}
	if err := yaml.Unmarshal(body, &manifest); err != nil {
		t.Fatal(err)
	}
	catalog := ProductCatalog()
	assertValuesEqual(t, "root languages", manifest.Factory.Languages, choiceValues(catalog.Languages))
	assertValuesEqual(t, "root databases", manifest.Factory.Databases, choiceValues(catalog.Databases))
	assertValuesEqual(t, "root deployments", manifest.Factory.Deployments, choiceValues(catalog.Deployments))
	assertValuesEqual(t, "root deliveries", manifest.Factory.Deliveries, choiceValues(catalog.Deliveries))
}

func assertValuesEqual(t *testing.T, name string, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("%s = %v, want %v", name, got, want)
	}
}

func TestCatalogAndTemplatePacksAgree(t *testing.T) {
	catalog := ProductCatalog()
	assertPackDirectories(t, "templates/languages", choiceValues(catalog.Languages))
	assertPackDirectories(t, "templates/deployments", choiceValues(catalog.Deployments))
	for _, language := range catalog.Languages {
		expected := append([]string{"base"}, choiceValues(catalog.Databases)...)
		root := "templates/languages/" + language.Value
		assertPackDirectories(t, root, expected)
		assertPackHasTemplates(t, root+"/base")
		assertLanguageHooks(t, root+"/base")
		for _, database := range catalog.Databases {
			assertPackHasTemplates(t, root+"/"+database.Value)
		}
	}
	for _, deployment := range catalog.Deployments {
		assertPackHasTemplates(t, "templates/deployments/"+deployment.Value)
	}
}

func TestSharedAndDeploymentTemplatesDoNotBranchOnLanguageMechanisms(t *testing.T) {
	assertTemplatesExclude(t, "templates/common", "if eq .Recipe.Service.Language")
	assertTemplatesExclude(t, "templates/deployments", ".Recipe.Service.Language")
	assertTemplatesExclude(t, "templates/languages", "if eq .Recipe.Database.Authority")
}

func TestPostgresPacksPreserveConcurrentCreateConflict(t *testing.T) {
	packs := map[string]string{
		"templates/languages/go/postgres/internal/store/postgres/postgres.go.tmpl": `postgresCode(err) == "23505"`,
		"templates/languages/typescript/postgres/src/store/postgres.ts.tmpl":       `postgresCode(error) === "23505"`,
		"templates/languages/python/postgres/reaper_flags/store/postgres.py.tmpl":  "psycopg.errors.UniqueViolation",
	}
	for path, conflictMarker := range packs {
		body, err := templateFiles.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		source := string(body)
		if strings.Contains(source, "ON CONFLICT") {
			t.Fatalf("%s turns concurrent creation into an upsert", path)
		}
		for _, required := range []string{"INSERT INTO flags", "UPDATE flags", conflictMarker} {
			if !strings.Contains(source, required) {
				t.Fatalf("%s is missing concurrent-create safeguard %q", path, required)
			}
		}
	}
}

func TestMongoDBPacksPreserveConcurrentCreateConflict(t *testing.T) {
	packs := map[string]struct {
		required  []string
		forbidden []string
	}{
		"templates/languages/go/mongodb/internal/store/mongodb/mongodb.go.tmpl": {
			required:  []string{"InsertOne", "UpdateOne", "mongo.IsDuplicateKeyError(err)"},
			forbidden: []string{"SetUpsert", "ReplaceOne", "FindOneAndReplace"},
		},
		"templates/languages/typescript/mongodb/src/store/mongodb.ts.tmpl": {
			required:  []string{"insertOne", "updateOne", "mongoCode(error) === 11000"},
			forbidden: []string{"upsert: true", "replaceOne", "findOneAndReplace"},
		},
		"templates/languages/python/mongodb/reaper_flags/store/mongodb.py.tmpl": {
			required:  []string{"insert_one", "update_one", "errors.DuplicateKeyError"},
			forbidden: []string{"upsert=True", "replace_one", "find_one_and_replace"},
		},
	}
	for path, contract := range packs {
		body, err := templateFiles.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		source := string(body)
		for _, marker := range contract.forbidden {
			if strings.Contains(source, marker) {
				t.Fatalf("%s turns concurrent creation into an upsert via %q", path, marker)
			}
		}
		for _, marker := range contract.required {
			if !strings.Contains(source, marker) {
				t.Fatalf("%s is missing concurrent-create safeguard %q", path, marker)
			}
		}
	}
}

func assertTemplatesExclude(t *testing.T, root, fragment string) {
	t.Helper()
	err := fs.WalkDir(templateFiles, root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		body, err := templateFiles.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(body), fragment) {
			t.Fatalf("template %s contains forbidden language mechanism %q", path, fragment)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func assertLanguageHooks(t *testing.T, base string) {
	t.Helper()
	for _, hook := range languageHookTemplates {
		path := base + "/" + hook
		entry, err := fs.Stat(templateFiles, path)
		if err != nil {
			t.Fatalf("language pack is missing stable hook %s: %v", path, err)
		}
		if entry.IsDir() {
			t.Fatalf("language hook is a directory: %s", path)
		}
	}
}

func TestLanguageAndDatabasePacksAreVersioned(t *testing.T) {
	for _, registered := range languagePacks {
		assertPackVersion(t, "language", registered)
	}
	for _, registered := range databasePacks {
		assertPackVersion(t, "database", registered.pack)
	}
}

func TestDeploymentPacksDeclareSafeCompatibility(t *testing.T) {
	for _, registered := range deploymentPacks {
		assertPackVersion(t, "deployment", registered.pack)
		assertDeploymentDatabases(t, registered)
		if registered.replicas.Default < 1 {
			t.Fatalf("deployment %q has invalid default replicas", registered.choice.Value)
		}
		if registered.replicas.Maximum > 0 && registered.replicas.Default > registered.replicas.Maximum {
			t.Fatalf("deployment %q defaults above its maximum", registered.choice.Value)
		}
	}
	for _, database := range databasePacks {
		if len(CompatibleDeployments(database.choice.Value)) == 0 {
			t.Fatalf("database %q has no compatible deployment", database.choice.Value)
		}
	}
}

func TestDeliveryPacksDeclareArtifactsAndPublishers(t *testing.T) {
	for _, registered := range deliveryPacks {
		assertPackVersion(t, "delivery", registered.pack)
		if !registered.artifacts.directory && registered.artifacts.archiveSuffix == "" {
			t.Fatalf("delivery %q declares no artifacts", registered.choice.Value)
		}
		if suffix := registered.artifacts.archiveSuffix; suffix != "" {
			if !strings.HasPrefix(suffix, ".") || strings.ContainsAny(suffix, `/\\`) {
				t.Fatalf("delivery %q has unsafe archive suffix %q", registered.choice.Value, suffix)
			}
		}
		if registered.publish == nil {
			t.Fatalf("delivery %q has no publisher", registered.choice.Value)
		}
	}
}

func assertDeploymentDatabases(t *testing.T, deployment deploymentPack) {
	t.Helper()
	if len(deployment.databases) == 0 {
		t.Fatalf("deployment %q supports no databases", deployment.choice.Value)
	}
	seen := make(map[string]struct{}, len(deployment.databases))
	for _, value := range deployment.databases {
		database, exists := findDatabase(value)
		if !exists {
			t.Fatalf("deployment %q names unknown database %q", deployment.choice.Value, value)
		}
		if _, duplicate := seen[value]; duplicate {
			t.Fatalf("deployment %q repeats database %q", deployment.choice.Value, value)
		}
		seen[value] = struct{}{}
		if deployment.requiresShared && !database.shared {
			t.Fatalf("deployment %q requires shared authority but accepts %q", deployment.choice.Value, value)
		}
		if !database.shared && (deployment.replicas.Default != 1 || deployment.replicas.Maximum != 1) {
			t.Fatalf(
				"deployment %q must pin non-shared authority %q to one replica",
				deployment.choice.Value,
				value,
			)
		}
	}
}

func TestCatalogCompatibilityIsDerivedFromRegistrations(t *testing.T) {
	catalog := ProductCatalog()
	for _, database := range catalog.Databases {
		want := choiceValues(CompatibleDeployments(database.Value))
		got := catalog.Compatibility.DeploymentsByDatabase[database.Value]
		if !slices.Equal(got, want) {
			t.Fatalf("deployments for %s = %v, want %v", database.Value, got, want)
		}
	}
	for _, deployment := range deploymentPacks {
		got, exists := catalog.Compatibility.ReplicasByDeployment[deployment.choice.Value]
		if !exists {
			t.Fatalf("deployment %q has no cataloged replica policy", deployment.choice.Value)
		}
		if got != deployment.replicas {
			t.Fatalf("replica policy for %q = %#v, want %#v", deployment.choice.Value, got, deployment.replicas)
		}
	}
}

func TestEveryCatalogedDeliveryFormatPublishes(t *testing.T) {
	for _, delivery := range ProductCatalog().Deliveries {
		t.Run(delivery.Value, func(t *testing.T) {
			recipe := DefaultRecipe()
			recipe.Name = "delivery-" + delivery.Value
			recipe.Delivery.Format = delivery.Value
			destination := filepath.Join(t.TempDir(), recipe.Name)
			result, err := Generate(recipe, destination)
			if err != nil {
				t.Fatal(err)
			}
			assertDeliveryResult(t, delivery.Value, destination, result)
		})
	}
}

func TestTemplateLayersCannotOverwriteEarlierOutput(t *testing.T) {
	data, err := newRenderData(DefaultRecipe())
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "README.md")
	const source = "templates/common/README.md.tmpl"
	if err := renderFile(source, destination, data); err != nil {
		t.Fatal(err)
	}
	if err := renderFile(source, destination, data); err == nil || !strings.Contains(err.Error(), "collision") {
		t.Fatalf("expected duplicate output to fail, got %v", err)
	}
}

func assertChoices(t *testing.T, choices []Choice) {
	t.Helper()
	if len(choices) == 0 {
		t.Fatal("catalog choice group is empty")
	}
	seen := make(map[string]struct{}, len(choices))
	for _, choice := range choices {
		if !optionValue.MatchString(choice.Value) {
			t.Fatalf("catalog value %q is not a path-safe identifier", choice.Value)
		}
		if strings.TrimSpace(choice.Label) == "" {
			t.Fatalf("catalog value %q has no label", choice.Value)
		}
		if strings.TrimSpace(choice.Description) == "" {
			t.Fatalf("catalog value %q has no description", choice.Value)
		}
		if _, exists := seen[choice.Value]; exists {
			t.Fatalf("catalog value %q is duplicated", choice.Value)
		}
		seen[choice.Value] = struct{}{}
	}
}

func assertPackVersion(t *testing.T, kind string, registered pack) {
	t.Helper()
	if !packVersion.MatchString(registered.version) {
		t.Fatalf("%s pack %q has invalid version %q", kind, registered.choice.Value, registered.version)
	}
}

func choiceValues(choices []Choice) []string {
	values := make([]string, 0, len(choices))
	for _, choice := range choices {
		values = append(values, choice.Value)
	}
	return values
}

func assertPackDirectories(t *testing.T, root string, expected []string) {
	t.Helper()
	entries, err := fs.ReadDir(templateFiles, root)
	if err != nil {
		t.Fatal(err)
	}
	actual := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			t.Fatalf("template pack root %s contains file %s", root, entry.Name())
		}
		actual = append(actual, entry.Name())
	}
	slices.Sort(actual)
	slices.Sort(expected)
	if !slices.Equal(actual, expected) {
		t.Fatalf("template packs under %s = %v, catalog requires %v", root, actual, expected)
	}
}

func assertPackHasTemplates(t *testing.T, root string) {
	t.Helper()
	count := 0
	err := fs.WalkDir(templateFiles, root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".tmpl") {
			t.Fatalf("template pack contains non-template file: %s", path)
		}
		count++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if count == 0 {
		t.Fatalf("template pack %s is empty", root)
	}
}

func assertDeliveryResult(t *testing.T, format, destination string, result Result) {
	t.Helper()
	if format == "directory" {
		assertPathExists(t, destination)
		assertPathMissing(t, destination+".zip")
		if result.Directory != destination || result.Archive != "" {
			t.Fatalf("directory result = %#v", result)
		}
		return
	}
	if format == "zip" {
		assertPathMissing(t, destination)
		assertPathExists(t, destination+".zip")
		if result.Directory != "" || result.Archive != destination+".zip" {
			t.Fatalf("ZIP result = %#v", result)
		}
		return
	}
	if format == "both" {
		assertPathExists(t, destination)
		assertPathExists(t, destination+".zip")
		if result.Directory != destination || result.Archive != destination+".zip" {
			t.Fatalf("combined result = %#v", result)
		}
		return
	}
	t.Fatalf("cataloged delivery %q has no publication contract", format)
}

func assertPathExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected %s to exist: %v", path, err)
	}
}

func assertPathMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected %s to be absent: %v", path, err)
	}
}
