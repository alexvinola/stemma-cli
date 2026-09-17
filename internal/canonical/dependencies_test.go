package canonical

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestProductionImportGraphKeepsCanonicalIndependent(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "list", "-deps", "-f", "{{if not .Standard}}{{.ImportPath}}{{end}}", ".")
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(),
		"GOTOOLCHAIN=local",
		"GOPROXY=off",
		"GOSUMDB=off",
		"GOWORK=off",
		"GOFLAGS=-mod=readonly",
		"GOCACHE="+t.TempDir(),
		"GOMODCACHE="+t.TempDir(),
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("list production dependencies of internal/canonical: %v\n%s", err, output)
	}

	const module = "github.com/alexvinola/stemma-cli"
	dependencies := strings.Fields(string(output))
	if len(dependencies) == 0 {
		t.Fatal("go list returned an empty production import graph")
	}
	foundCanonical := false
	for _, dependency := range dependencies {
		if dependency == module+"/internal/canonical" {
			foundCanonical = true
		}
		for _, forbidden := range []string{
			module + "/internal/parser",
			module + "/internal/adapters",
		} {
			if dependency == forbidden || strings.HasPrefix(dependency, forbidden+"/") {
				t.Errorf("production internal/canonical transitively imports %s", dependency)
			}
		}
	}
	if !foundCanonical {
		t.Errorf("production import graph does not include internal/canonical:\n%s", output)
	}
}
