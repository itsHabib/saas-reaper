package factory

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDogfoodWorkValidatorUsesGeneratedValidatorSource(t *testing.T) {
	root, err := os.ReadFile(filepath.Join("..", "..", "scripts", "check-work.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(root, []byte("internal/factory/templates/common/scripts/check-work.sh.tmpl")) {
		t.Fatal("dogfood wrapper does not execute the generated validator source")
	}
}

func TestGeneratedWorkContractIsTailoredAndValid(t *testing.T) {
	recipe := DefaultRecipe()
	recipe.Name = "northstar-flags"
	recipe.Service.Language = "typescript"
	recipe.Domain.Tenant = "workspace"
	recipe.Delivery.Format = "directory"
	root := generateWorkRepository(t, recipe)

	assertFileContains(t, filepath.Join(root, "WORK.md"), "# Work: Adapt workspace feature flags")
	assertFileContains(t, filepath.Join(root, "WORK.md"), "Work-ID: adapt-northstar-flags-domain")
	assertFileContains(t, filepath.Join(root, "WORK.md"), "`src/flags/`")
	assertFileContains(t, filepath.Join(root, "WORK.md"), "`src/flags/evaluate.test.ts`")
	assertFileContains(t, filepath.Join(root, "WORK.md"), "Subject: recipe:sha256:")

	stdout, stderr, err := runWorkCheck(root)
	if err != nil {
		t.Fatalf("work check failed: %v\n%s", err, stderr)
	}
	for _, fragment := range []string{
		`"schema":"reaper-work/v1"`,
		`"workId":"adapt-northstar-flags-domain"`,
		`"status":"ready"`,
		`"verdict":"pass"`,
	} {
		if !strings.Contains(stdout, fragment) {
			t.Fatalf("work check output %q does not contain %q", stdout, fragment)
		}
	}
}

func TestWorkContractRedControls(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(string) string
		failure string
	}{
		{
			name: "recipe subject mismatch",
			mutate: func(body string) string {
				line := workMetadataLine(body, "Subject")
				mutant := "Subject: recipe:sha256:" + strings.Repeat("0", 64)
				return strings.Replace(body, line, mutant, 1)
			},
			failure: "work_contract:subject_mismatch",
		},
		{
			name: "done with pending evidence",
			mutate: func(body string) string {
				return strings.Replace(body, "Status: ready", "Status: done", 1)
			},
			failure: "work_contract:done_needs_verified_evidence",
		},
		{
			name: "missing adversarial proof",
			mutate: func(body string) string {
				return strings.Replace(body, "- Red:", "- Rejection:", 1)
			},
			failure: "work_contract:red_proof_missing",
		},
		{
			name: "broad change surface",
			mutate: func(body string) string {
				return strings.Replace(body, "- `DOMAIN.md`:", "- domain files:", 1)
			},
			failure: "work_contract:change_path",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recipe := DefaultRecipe()
			recipe.Name = "mutant-flags"
			recipe.Delivery.Format = "directory"
			root := generateWorkRepository(t, recipe)
			path := filepath.Join(root, "WORK.md")
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			mutant := test.mutate(string(body))
			if mutant == string(body) {
				t.Fatal("mutator did not change WORK.md")
			}
			if err := os.WriteFile(path, []byte(mutant), 0o644); err != nil {
				t.Fatal(err)
			}
			_, stderr, err := runWorkCheck(root)
			if err == nil {
				t.Fatal("mutant work contract passed")
			}
			if !strings.Contains(stderr, test.failure) {
				t.Fatalf("failure = %q, want %q", stderr, test.failure)
			}
		})
	}
}

func generateWorkRepository(t *testing.T, recipe Recipe) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), recipe.Name)
	result, err := Generate(recipe, root)
	if err != nil {
		t.Fatal(err)
	}
	return result.Directory
}

func runWorkCheck(root string) (string, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "bash", "scripts/check-work.sh", "--json")
	command.Dir = root
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	return stdout.String(), stderr.String(), err
}

func workMetadataLine(body, name string) string {
	prefix := name + ": "
	for line := range strings.SplitSeq(body, "\n") {
		if strings.HasPrefix(line, prefix) {
			return line
		}
	}
	return ""
}
