package cli_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/alexvinola/stemma-cli/internal/cli"
	"github.com/alexvinola/stemma-cli/internal/compiler"
	"github.com/alexvinola/stemma-cli/internal/diagnostics"
)

func TestApplyPreservesPlanDiagnostics(t *testing.T) {
	for _, mode := range []string{"direct", "saved", "unchanged", "empty"} {
		for _, jsonOut := range []bool{false, true} {
			name := mode + "/human"
			if jsonOut {
				name = mode + "/json"
			}
			t.Run(name, func(t *testing.T) {
				h := newHarness(t)
				if mode == "empty" {
					// A target not enabled in the project still warns without outputs.
					h.write(".stemma/project.json", `{"schemaVersion":2,"id":"prj_test","name":"Test","targets":["claude"]}`)
				} else {
					h.fromFixture("claude/basic")
					if res := h.run("import", "--from", "claude"); res.code != cli.ExitOK {
						t.Fatalf("import: %+v", res)
					}
				}
				if mode == "unchanged" {
					if res := h.run("apply", "--target", "codex", "--yes"); res.code != cli.ExitOK {
						t.Fatalf("first apply: %+v", res)
					}
				}
				planned := h.run("plan", "--target", "codex", "--output-plan", "plan.json", "--json")
				if planned.code != cli.ExitOK {
					t.Fatalf("plan: %+v", planned)
				}
				var planDoc cli.Envelope
				if err := json.Unmarshal([]byte(planned.stdout), &planDoc); err != nil {
					t.Fatal(err)
				}
				if len(planDoc.Diagnostics) == 0 {
					t.Fatal("test needs non-blocking plan diagnostics")
				}
				args := []string{"apply", "--target", "codex", "--yes"}
				if mode == "saved" {
					args = []string{"apply", "--plan", "plan.json", "--yes"}
				}
				if jsonOut {
					args = append(args, "--json")
				}
				applied := h.run(args...)
				if applied.code != cli.ExitOK {
					t.Fatalf("apply: %+v", applied)
				}
				if jsonOut {
					var doc struct {
						Diagnostics []diagnostics.Diagnostic `json:"diagnostics"`
						Data        compiler.ApplyResult     `json:"data"`
					}
					if err := json.Unmarshal([]byte(applied.stdout), &doc); err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(doc.Diagnostics, planDoc.Diagnostics) || !reflect.DeepEqual(doc.Data.Diagnostics, planDoc.Diagnostics) {
						t.Fatalf("diagnostics differ: plan=%+v envelope=%+v result=%+v", planDoc.Diagnostics, doc.Diagnostics, doc.Data.Diagnostics)
					}
				} else {
					var expected strings.Builder
					cli.PrintDiagnostics(&expected, planDoc.Diagnostics, true)
					if !strings.Contains(applied.stdout, expected.String()) {
						t.Fatalf("apply lost diagnostics:\n%s\nwant:\n%s", applied.stdout, expected.String())
					}
				}
			})
		}
	}
}

func TestApplyShowsWarningsBeforeConfirmation(t *testing.T) {
	for _, answer := range []string{"y\n", "n\n"} {
		t.Run(strings.TrimSpace(answer), func(t *testing.T) {
			h := newHarness(t)
			h.fromFixture("claude/basic")
			if res := h.run("import", "--from", "claude"); res.code != cli.ExitOK {
				t.Fatalf("import: %+v", res)
			}
			before := h.snapshot()
			res := h.runWith(answer, true, "apply", "--target", "codex")
			if res.code != cli.ExitOK {
				t.Fatalf("apply: %+v", res)
			}
			warning := strings.Index(res.stdout, "Warnings (")
			prompt := strings.Index(res.stdout, "Apply these changes?")
			if warning < 0 || prompt < 0 || warning > prompt {
				t.Fatalf("warnings must precede confirmation:\n%s", res.stdout)
			}
			if strings.Count(res.stdout, "Warnings (") != 1 {
				t.Fatalf("warnings printed more than once:\n%s", res.stdout)
			}
			if answer == "n\n" {
				assertSameTree(t, before, h.snapshot(), "cancelled apply with warnings")
			}
		})
	}
}

// confirmationReader introduces a filesystem change between planning and apply.
type confirmationReader func([]byte) (int, error)

func (f confirmationReader) Read(p []byte) (int, error) { return f(p) }

func TestApplyWarningsDoNotChangeWriteFailureExitCode(t *testing.T) {
	h := newHarness(t)
	h.fromFixture("claude/basic")
	if res := h.run("import", "--from", "claude"); res.code != cli.ExitOK {
		t.Fatalf("import: %+v", res)
	}
	var out, errOut strings.Builder
	answer := strings.NewReader("y\n")
	input := confirmationReader(func(p []byte) (int, error) {
		// A directory cannot be hashed as an output file. This fails before
		// the transaction can add its own diagnostic, while plan warnings exist.
		if answer.Len() == 2 {
			if err := os.Mkdir(filepath.Join(h.root, "AGENTS.md"), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		return answer.Read(p)
	})
	code := cli.Run(context.Background(), cli.Env{
		Stdout: &out, Stderr: &errOut, Stdin: input, StdinIsTTY: true, WorkingDir: h.root,
	}, []string{"apply", "--target", "codex"})
	if code != cli.ExitWriteFailed {
		t.Fatalf("exit = %d, want %d: %s", code, cli.ExitWriteFailed, errOut.String())
	}
	if !strings.Contains(errOut.String(), string(diagnostics.AgentNotNative)) {
		t.Fatalf("write failure lost the compilation warning: %s", errOut.String())
	}
	if h.exists(".agents/skills/skill-migration/SKILL.md") {
		t.Fatal("failed apply wrote a generated skill")
	}
}
