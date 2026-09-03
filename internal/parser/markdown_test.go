package parser

import (
	"strings"
	"testing"
)

func TestParseFrontMatterBasics(t *testing.T) {
	src := "---\n" +
		"description: API rules\n" +
		"applyTo: \"src/api/**,src/lib/**\"\n" +
		"paths:\n" +
		"  - src/api/**\n" +
		"  - src/lib/**\n" +
		"enabled: false\n" +
		"count: 3\n" +
		"nested:\n" +
		"  key: value\n" +
		"body: |\n" +
		"  line one\n" +
		"  line two\n" +
		"---\n" +
		"# Title\n\nSome text.\n"
	doc := Parse("x.md", []byte(src))
	for _, d := range doc.Diagnostics {
		if d.Severity == "error" {
			t.Fatalf("unexpected error diagnostic: %+v", d)
		}
	}
	if doc.FrontMatter == nil {
		t.Fatal("expected front matter")
	}
	if got, _ := doc.FrontMatter.String("description"); got != "API rules" {
		t.Errorf("description = %q", got)
	}
	if got, _ := doc.FrontMatter.String("applyTo"); got != "src/api/**,src/lib/**" {
		t.Errorf("applyTo = %q", got)
	}
	paths, ok := doc.FrontMatter.StringList("paths")
	if !ok || len(paths) != 2 || paths[0] != "src/api/**" {
		t.Errorf("paths = %v ok=%v", paths, ok)
	}
	if v, ok := doc.FrontMatter.Bool("enabled"); !ok || v {
		t.Errorf("enabled = %v ok=%v", v, ok)
	}
	if doc.FrontMatter.Fields["count"] != int64(3) {
		t.Errorf("count = %#v", doc.FrontMatter.Fields["count"])
	}
	nested, ok := doc.FrontMatter.Fields["nested"].(map[string]any)
	if !ok || nested["key"] != "value" {
		t.Errorf("nested = %#v", doc.FrontMatter.Fields["nested"])
	}
	if got := doc.FrontMatter.Fields["body"]; got != "line one\nline two\n" {
		t.Errorf("block scalar = %q", got)
	}
	if doc.Title != "Title" {
		t.Errorf("title = %q", doc.Title)
	}
	if doc.BodyLine != 15 {
		t.Errorf("body line = %d", doc.BodyLine)
	}
}

func TestFenceAwareness(t *testing.T) {
	src := "Intro\n\n```md\n# Not a heading\n---\n- not a bullet\n```\n\n## Real heading\n\n- real bullet\n"
	doc := Parse("x.md", []byte(src))
	if doc.FrontMatter != nil {
		t.Fatal("no front matter expected")
	}
	var headings []string
	for _, s := range doc.Sections {
		if s.Level > 0 {
			headings = append(headings, s.Heading)
		}
	}
	if len(headings) != 1 || headings[0] != "Real heading" {
		t.Fatalf("headings = %v", headings)
	}
	bullets := Bullets(doc.Sections[0].Content, 1)
	if len(bullets) != 0 {
		t.Errorf("preamble bullets = %#v", bullets)
	}
	bullets = Bullets(doc.Sections[1].Content, 1)
	if len(bullets) != 1 || bullets[0].Text != "real bullet" {
		t.Errorf("bullets = %#v", bullets)
	}
}

func TestFrontMatterInsideFenceIsNotFrontMatter(t *testing.T) {
	src := "# Doc\n\n```yaml\n---\nkey: value\n---\n```\n"
	doc := Parse("x.md", []byte(src))
	if doc.FrontMatter != nil {
		t.Fatal("fenced --- must not be parsed as front matter")
	}
}

func TestUnterminatedFrontMatterIsPreserved(t *testing.T) {
	src := "---\nkey: value\n\nbody\n"
	doc := Parse("x.md", []byte(src))
	if doc.FrontMatter != nil {
		t.Fatal("expected no front matter")
	}
	if doc.Body != src {
		t.Fatal("body must be preserved verbatim")
	}
	if len(doc.Diagnostics) == 0 {
		t.Fatal("expected a diagnostic")
	}
}

func TestUnsafeYAMLRejected(t *testing.T) {
	for _, src := range []string{
		"---\nkey: !!python/object/apply:os.system [\"ls\"]\n---\n",
		"---\nbase: &anchor\n---\n",
		"---\nkey: *alias\n---\n",
		"---\n<<: *defaults\n---\n",
	} {
		doc := Parse("x.md", []byte(src))
		found := false
		for _, d := range doc.Diagnostics {
			if d.Severity == "error" {
				found = true
			}
		}
		if !found {
			t.Errorf("expected an error diagnostic for %q, got %+v", src, doc.Diagnostics)
		}
	}
}

func TestCRLFAndBOM(t *testing.T) {
	src := "\xef\xbb\xbf---\r\ndescription: x\r\n---\r\n# Title\r\n\r\nBody\r\n"
	doc := Parse("x.md", []byte(src))
	if !doc.HasBOM {
		t.Error("expected BOM detection")
	}
	if doc.LineEnding != LineEndingCRLF {
		t.Errorf("line ending = %q", doc.LineEnding)
	}
	if doc.FrontMatter == nil {
		t.Fatal("expected front matter with CRLF")
	}
	if got, _ := doc.FrontMatter.String("description"); got != "x" {
		t.Errorf("description = %q", got)
	}
	if !strings.Contains(doc.Body, "# Title") {
		t.Errorf("body = %q", doc.Body)
	}
}

func TestInvalidUTF8Rejected(t *testing.T) {
	doc := Parse("x.md", []byte{0xff, 0xfe, 'a'})
	if len(doc.Diagnostics) == 0 || doc.Diagnostics[0].Severity != "error" {
		t.Fatalf("expected an encoding error, got %+v", doc.Diagnostics)
	}
}

func TestSectionSpans(t *testing.T) {
	src := "# A\n\nalpha\n\n## B\n\nbeta\n"
	doc := Parse("x.md", []byte(src))
	if len(doc.Sections) != 2 {
		t.Fatalf("sections = %d", len(doc.Sections))
	}
	if doc.Sections[0].Content != "alpha" || doc.Sections[1].Content != "beta" {
		t.Fatalf("contents = %q / %q", doc.Sections[0].Content, doc.Sections[1].Content)
	}
	if doc.Sections[1].HeadingLine != 5 {
		t.Errorf("heading line = %d", doc.Sections[1].HeadingLine)
	}
	if got := src[doc.Sections[1].Span.ByteStart:doc.Sections[1].Span.ByteEnd]; got != "## B\n\nbeta\n" {
		t.Errorf("span slice = %q", got)
	}
}
