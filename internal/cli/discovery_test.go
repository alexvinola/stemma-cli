package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexvinola/stemma-cli/internal/cli"
)

// deepPath is one directory level deeper than the default depth limit (32).
var deepPath = strings.Repeat("d/", 33) + "CLAUDE.md"

func TestIncompleteScanBlocksImportUnlessFlagged(t *testing.T) {
	h := newHarness(t)
	h.write("CLAUDE.md", "# Project\n\n## Style\n\nUse tabs.\n")
	h.write(deepPath, "# Deep\n\nNever discovered.\n")

	before := h.snapshot()
	res := h.run("import")
	if res.code != cli.ExitDiagnostics {
		t.Fatalf("exit = %d, want %d\n%s", res.code, cli.ExitDiagnostics, res.stderr)
	}
	if !strings.Contains(res.stderr, "STEMMA1303_DISCOVERY_INCOMPLETE") ||
		!strings.Contains(res.stderr, "--allow-incomplete-scan") {
		t.Errorf("stderr should explain the refusal and the override:\n%s", res.stderr)
	}
	assertSameTree(t, before, h.snapshot(), "a refused import")

	res = h.run("import", "--json")
	if res.code != cli.ExitDiagnostics {
		t.Fatalf("json exit = %d", res.code)
	}
	var doc struct {
		Diagnostics []struct {
			Code     string `json:"code"`
			Blocking bool   `json:"blocking"`
		} `json:"diagnostics"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &doc); err != nil {
		t.Fatalf("json: %v\n%s", err, res.stdout)
	}
	blocking := false
	for _, d := range doc.Diagnostics {
		blocking = blocking || (d.Code == "STEMMA1303_DISCOVERY_INCOMPLETE" && d.Blocking)
	}
	if !blocking {
		t.Errorf("json output lacks the blocking STEMMA1303 diagnostic: %s", res.stdout)
	}

	res = h.run("import", "--allow-incomplete-scan")
	if res.code != cli.ExitOK {
		t.Fatalf("allowed import exit = %d\n%s%s", res.code, res.stdout, res.stderr)
	}
	if !strings.Contains(res.stdout, "STEMMA1303_DISCOVERY_INCOMPLETE") {
		t.Errorf("an allowed incomplete import must still say so:\n%s", res.stdout)
	}
}

func TestScanReportsIncompleteDiscovery(t *testing.T) {
	h := newHarness(t)
	h.write("CLAUDE.md", "# Project\n")
	h.write(deepPath, "# Deep\n")
	res := h.run("scan")
	if res.code != cli.ExitOK {
		t.Fatalf("exit = %d", res.code)
	}
	for _, want := range []string{"Scan INCOMPLETE", "max-depth", "--allow-incomplete-scan", "STEMMA1002_LIMIT_REACHED"} {
		if !strings.Contains(res.stdout, want) {
			t.Errorf("scan output lacks %q:\n%s", want, res.stdout)
		}
	}

	res = h.run("scan", "--json")
	var doc struct {
		Data struct {
			Complete      bool     `json:"complete"`
			LimitsReached []string `json:"limitsReached"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &doc); err != nil {
		t.Fatalf("json: %v\n%s", err, res.stdout)
	}
	if doc.Data.Complete || len(doc.Data.LimitsReached) != 1 || doc.Data.LimitsReached[0] != "max-depth" {
		t.Errorf("json scan = %+v", doc.Data)
	}
}

func TestScanNamesProvidersThatAlsoReadAFile(t *testing.T) {
	h := newHarness(t)
	h.write("AGENTS.md", "# Project\n\n## Style\n\nUse tabs.\n")
	res := h.run("scan")
	if res.code != cli.ExitOK {
		t.Fatalf("exit = %d", res.code)
	}
	if !strings.Contains(res.stdout, "codex") || !strings.Contains(res.stdout, "(also read by kiro)") {
		t.Errorf("scan output should detect codex and name kiro as a reader:\n%s", res.stdout)
	}
	if strings.Contains(res.stdout, "INCOMPLETE") {
		t.Errorf("a complete scan must not be reported as incomplete:\n%s", res.stdout)
	}
}

func TestScanAndImportReportUnreadableDirectory(t *testing.T) {
	h := newHarness(t)
	h.write("CLAUDE.md", "# Project\n\n## Style\n\nUse tabs.\n")
	h.write("hidden/CLAUDE.md", "# Hidden\n")
	hidden := filepath.Join(h.root, "hidden")
	if err := os.Chmod(hidden, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(hidden, 0o755) })
	if _, err := os.ReadDir(hidden); err == nil {
		t.Skip("directory permissions are not enforced for this user")
	}
	res := h.run("scan")
	for _, want := range []string{"Scan INCOMPLETE: 1 unreadable directory", "STEMMA1305_DIRECTORY_UNREADABLE"} {
		if !strings.Contains(res.stdout, want) {
			t.Errorf("scan output lacks %q:\n%s", want, res.stdout)
		}
	}
	res = h.run("import")
	if res.code != cli.ExitDiagnostics || !strings.Contains(res.stderr, "STEMMA1303_DISCOVERY_INCOMPLETE") {
		t.Errorf("import exit = %d, stderr:\n%s", res.code, res.stderr)
	}
	if h.exists(".stemma/project.json") {
		t.Error("a refused import wrote the project")
	}
}
