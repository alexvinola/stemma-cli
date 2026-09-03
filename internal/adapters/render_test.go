package adapters

import (
	"strings"
	"testing"

	"github.com/alexvinola/stemma-cli/internal/canonical"
	"github.com/alexvinola/stemma-cli/internal/parser"
	"github.com/alexvinola/stemma-cli/internal/profiles"
)

func TestRenderFrontMatterIsParseableBack(t *testing.T) {
	entries := []KV{
		{Key: "applyTo", Value: "src/api/**,src/lib/**"},
		{Key: "paths", Value: []string{"src/**/*.go", "docs/*.md"}},
		{Key: "inclusion", Value: "fileMatch"},
		{Key: "enabled", Value: false},
		{Key: "count", Value: 3},
		{Key: "nested", Value: map[string]any{"b": "2", "a": "1"}},
		{Key: "tricky", Value: "value: with colon # and hash"},
	}
	rendered := RenderFrontMatter(entries)
	doc := parser.Parse("x.md", []byte(rendered+"# Title\n\nbody\n"))
	for _, d := range doc.Diagnostics {
		if d.Severity == "error" {
			t.Fatalf("rendered front matter does not parse: %+v\n%s", d, rendered)
		}
	}
	if doc.FrontMatter == nil {
		t.Fatalf("no front matter parsed from:\n%s", rendered)
	}
	if got, _ := doc.FrontMatter.String("applyTo"); got != "src/api/**,src/lib/**" {
		t.Errorf("applyTo = %q", got)
	}
	paths, ok := doc.FrontMatter.StringList("paths")
	if !ok || len(paths) != 2 || paths[0] != "src/**/*.go" {
		t.Errorf("paths = %v", paths)
	}
	if v, ok := doc.FrontMatter.Bool("enabled"); !ok || v {
		t.Errorf("enabled = %v %v", v, ok)
	}
	if got, _ := doc.FrontMatter.String("tricky"); got != "value: with colon # and hash" {
		t.Errorf("tricky = %q", got)
	}
	nested, _ := doc.FrontMatter.Fields["nested"].(map[string]any)
	if nested["a"] != "1" || nested["b"] != "2" {
		t.Errorf("nested = %#v", doc.FrontMatter.Fields["nested"])
	}
}

func TestRenderFrontMatterIsDeterministic(t *testing.T) {
	value := map[string]any{"z": 1, "a": 2, "m": 3}
	first := RenderFrontMatter([]KV{{Key: "x", Value: value}})
	for i := 0; i < 50; i++ {
		if RenderFrontMatter([]KV{{Key: "x", Value: value}}) != first {
			t.Fatal("map rendering depends on iteration order")
		}
	}
}

func TestRenderFrontMatterEmpty(t *testing.T) {
	if RenderFrontMatter(nil) != "" {
		t.Error("no entries must render nothing at all")
	}
}

func TestExtensionEntriesSkipsInternalKeys(t *testing.T) {
	var ext canonical.Extensions
	ext.Set("claude", "stemma.ruleFile", "api.md")
	ext.Set("claude", "custom", "value")
	ext.Set("claude", "description", "desc")
	entries := ExtensionEntries(ext, "claude", "description")
	if len(entries) != 1 || entries[0].Key != "custom" {
		t.Fatalf("entries = %+v", entries)
	}
}

func TestMarkdownBuilder(t *testing.T) {
	var md Markdown
	md.Heading(1, "Title")
	md.Paragraph("First paragraph.")
	md.Heading(2, "Section")
	md.Bullet("one")
	md.Bullet("two\ncontinued")
	got := md.String()
	want := "# Title\n\nFirst paragraph.\n\n## Section\n\n- one\n- two\n  continued\n"
	if got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
	if !strings.HasSuffix(got, "\n") || strings.HasSuffix(got, "\n\n") {
		t.Error("output must end with exactly one newline")
	}
}

func TestMarkdownEmpty(t *testing.T) {
	var md Markdown
	if !md.Empty() || md.String() != "" {
		t.Error("an empty document must render as an empty string")
	}
}

func TestSplitDocumentUsesTitleAndSubsections(t *testing.T) {
	src := "# Project\n\nIntro text.\n\n## Architecture\n\nLayers.\n\n### Details\n\nMore.\n\n## Testing\n\nRun tests.\n"
	doc := parser.Parse("x.md", []byte(src))
	units := SplitDocument(doc)
	if len(units) != 3 {
		t.Fatalf("units = %d: %+v", len(units), units)
	}
	if units[0].Title != "" || units[0].Content != "Intro text." {
		t.Errorf("preamble = %+v", units[0])
	}
	if units[1].Title != "Architecture" || !strings.Contains(units[1].Content, "### Details") {
		t.Errorf("subsections must stay inside their parent unit: %+v", units[1])
	}
	if units[2].Title != "Testing" {
		t.Errorf("third unit = %+v", units[2])
	}
}

func TestSplitDocumentWithoutTitle(t *testing.T) {
	src := "## A\n\nalpha\n\n## B\n\nbeta\n"
	units := SplitDocument(parser.Parse("x.md", []byte(src)))
	if len(units) != 2 || units[0].Title != "A" || units[1].Title != "B" {
		t.Fatalf("units = %+v", units)
	}
}

func TestSplitDocumentPlainText(t *testing.T) {
	units := SplitDocument(parser.Parse("x.md", []byte("Just prose, no headings.\n")))
	if len(units) != 1 || units[0].Content != "Just prose, no headings." {
		t.Fatalf("units = %+v", units)
	}
}

func TestKindFromHeading(t *testing.T) {
	if got := KindFromHeading("Architecture"); got != canonical.KindArchitecture {
		t.Errorf("Architecture -> %s", got)
	}
	if got := KindFromHeading("  TESTING  "); got != canonical.KindTesting {
		t.Errorf("TESTING -> %s", got)
	}
	// Anything not in the table stays "other": no inference from prose.
	if got := KindFromHeading("Some thoughts about our approach"); got != canonical.KindOther {
		t.Errorf("unknown heading -> %s, want other", got)
	}
}

func TestScopeLabel(t *testing.T) {
	cases := map[string]canonical.Activation{
		"always-on": canonical.Always(),
		"paths src/** (excluding src/**/*_test.go)": canonical.PathScoped([]string{"src/**"}, []string{"src/**/*_test.go"}),
		"on demand (release)":                       canonical.OnDemand("", "release"),
		"documentation only":                        canonical.DocumentationOnly(),
	}
	for want, activation := range cases {
		if got := ScopeLabel(activation); got != want {
			t.Errorf("ScopeLabel = %q, want %q", got, want)
		}
	}
}

func TestResolveHonoursCanonicalEnablement(t *testing.T) {
	yes := true
	profile := profileWithOverride("rule.x", yes)
	res := Resolve("rule.x", false, canonical.Always(), "text", profile)
	if res.Included {
		t.Error("a profile must not resurrect a rule disabled in the canonical project")
	}
}

func TestResolveSkipsDocumentationOnly(t *testing.T) {
	res := Resolve("context.x", true, canonical.DocumentationOnly(), "text", emptyProfile())
	if res.Included {
		t.Error("documentation-only content must never be projected by default")
	}
	if res.SkippedReason == "" {
		t.Error("a skipped entity must carry a reason")
	}
}

func emptyProfile() profiles.Profile { return profiles.Default(canonical.TargetClaude) }

func profileWithOverride(id string, include bool) profiles.Profile {
	p := profiles.Default(canonical.TargetClaude)
	p.Overrides[id] = profiles.Override{Include: &include}
	return p
}
