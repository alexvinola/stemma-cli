package compiler_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/alexvinola/stemma/internal/adapters"
	"github.com/alexvinola/stemma/internal/canonical"
	"github.com/alexvinola/stemma/internal/compiler"
	"github.com/alexvinola/stemma/internal/version"
)

func readSchema(t *testing.T, name string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("../../schema", name))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("%s is not valid JSON: %v", name, err)
	}
	return doc
}

// TestSchemasMatchTheCode keeps the published schemas honest: the documented
// enums and versions must match what the compiler actually produces.
func TestSchemasMatchTheCode(t *testing.T) {
	canonicalSchema := readSchema(t, "canonical-v1.schema.json")
	props := canonicalSchema["properties"].(map[string]any)
	if got := props["schemaVersion"].(map[string]any)["const"].(float64); int(got) != version.CanonicalSchemaVersion {
		t.Errorf("canonical schema version = %v, code says %d", got, version.CanonicalSchemaVersion)
	}
	defs := canonicalSchema["$defs"].(map[string]any)
	assertEnum(t, "canonical targetFormat", defs["targetFormat"], targetStrings())
	assertEnum(t, "context kind",
		defs["contextDocument"].(map[string]any)["properties"].(map[string]any)["kind"], kindStrings())
	assertEnum(t, "activation type",
		defs["activation"].(map[string]any)["properties"].(map[string]any)["type"], activationStrings())

	profileSchema := readSchema(t, "profile-v1.schema.json")
	if got := profileSchema["properties"].(map[string]any)["schemaVersion"].(map[string]any)["const"].(float64); int(got) != version.ProfileSchemaVersion {
		t.Errorf("profile schema version = %v, code says %d", got, version.ProfileSchemaVersion)
	}
	assertEnum(t, "profile target",
		profileSchema["properties"].(map[string]any)["target"], targetStrings())

	reportSchema := readSchema(t, "report-v1.schema.json")
	if got := reportSchema["properties"].(map[string]any)["schemaVersion"].(map[string]any)["const"].(float64); int(got) != version.ReportSchemaVersion {
		t.Errorf("report schema version = %v, code says %d", got, version.ReportSchemaVersion)
	}
	reportDefs := reportSchema["$defs"].(map[string]any)
	assertEnum(t, "projection outcome",
		reportDefs["projectionMapping"].(map[string]any)["properties"].(map[string]any)["outcome"],
		[]string{
			string(adapters.OutcomeAdapted), string(adapters.OutcomeBlocked), string(adapters.OutcomeExact),
			string(adapters.OutcomeLossy), string(adapters.OutcomeSkipped),
		})
	assertEnum(t, "change kind",
		reportDefs["change"].(map[string]any)["properties"].(map[string]any)["kind"],
		[]string{
			string(compiler.ChangeConflict), string(compiler.ChangeCreate),
			string(compiler.ChangeDeleteProposed), string(compiler.ChangeUnchanged),
			string(compiler.ChangeUpdate),
		})
	assertEnum(t, "entity type",
		reportDefs["projectionMapping"].(map[string]any)["properties"].(map[string]any)["entityType"],
		entityTypeStrings())
}

func assertEnum(t *testing.T, what string, node any, want []string) {
	t.Helper()
	m, ok := node.(map[string]any)
	if !ok {
		t.Fatalf("%s: schema node is not an object", what)
	}
	raw, ok := m["enum"].([]any)
	if !ok {
		t.Fatalf("%s: schema node has no enum", what)
	}
	got := make([]string, 0, len(raw))
	for _, v := range raw {
		got = append(got, v.(string))
	}
	sort.Strings(got)
	sorted := append([]string{}, want...)
	sort.Strings(sorted)
	if len(got) != len(sorted) {
		t.Fatalf("%s: schema enum %v, code %v", what, got, sorted)
	}
	for i := range got {
		if got[i] != sorted[i] {
			t.Fatalf("%s: schema enum %v, code %v", what, got, sorted)
		}
	}
}

func targetStrings() []string {
	out := make([]string, 0)
	for _, t := range canonical.AllTargets() {
		out = append(out, string(t))
	}
	return out
}

func kindStrings() []string {
	out := make([]string, 0)
	for _, k := range canonical.AllContextKinds() {
		out = append(out, string(k))
	}
	return out
}

func entityTypeStrings() []string {
	out := make([]string, 0)
	for _, e := range canonical.AllEntityTypes() {
		out = append(out, string(e))
	}
	return out
}

func activationStrings() []string {
	return []string{
		string(canonical.ActivationAlways), string(canonical.ActivationPathScoped),
		string(canonical.ActivationOnDemand), string(canonical.ActivationDocumentationOnly),
	}
}
