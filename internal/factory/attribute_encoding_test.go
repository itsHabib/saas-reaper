package factory

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestAttributeLiteralsPreserveRecipeData(t *testing.T) {
	recipe := DefaultRecipe()
	recipe.Domain.TargetingAttributes = []string{"targetingKey", "quote\"here", "back\\slash", "bell\a", "emoji😀"}
	data, err := newRenderData(recipe)
	if err != nil {
		t.Fatal(err)
	}
	var decoded []string
	if err := json.Unmarshal([]byte("["+data.Attributes+"]"), &decoded); err != nil {
		t.Fatalf("attribute literals are not portable JSON: %v", err)
	}
	if !reflect.DeepEqual(decoded, recipe.Domain.TargetingAttributes) {
		t.Fatalf("attributes changed: %#v", decoded)
	}
}

func TestGeneratedPythonImportsQuotedAttributesAsData(t *testing.T) {
	recipe := DefaultRecipe()
	recipe.Service.Language = "python"
	recipe.Domain.TargetingAttributes = []string{"targetingKey", "quote\"here", "back\\slash", "line\nbreak"}
	result, err := Generate(recipe, filepath.Join(t.TempDir(), "quoted-attributes"))
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), "python3", "-c", "import json; from reaper_flags.flags.flag import APPROVED_ATTRIBUTES; print(json.dumps(sorted(APPROVED_ATTRIBUTES)))")
	command.Dir = result.Directory
	command.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("generated Python cannot import: %v\n%s", err, output)
	}
	var decoded []string
	if err := json.Unmarshal(output, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded) != len(recipe.Domain.TargetingAttributes) {
		t.Fatalf("attributes changed: %#v", decoded)
	}
	for _, attribute := range decoded {
		found := false
		for _, expected := range recipe.Domain.TargetingAttributes {
			found = found || attribute == expected
		}
		if !found {
			t.Fatalf("unexpected attribute %q", attribute)
		}
	}
}
