package parser

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func FuzzParse(f *testing.F) {
	f.Add("# Title\n\nBody\n")
	f.Add("---\nkey: value\n---\n\n# T\n")
	f.Add("---\n")
	f.Add("```\n---\nnot: front matter\n```\n")
	f.Add("\xef\xbb\xbf---\r\na: b\r\n---\r\n")
	f.Add("---\nkey: !!tag value\n---\n")
	f.Add("- a\n  - b\n")
	f.Add(strings.Repeat("#", 100) + " heading\n")
	f.Fuzz(func(t *testing.T, input string) {
		doc := Parse("fuzz.md", []byte(input))

		// Parsing never panics and always yields a consistent document.
		if doc.FrontMatter != nil {
			if doc.FrontMatter.ByteEnd > len(doc.Text) {
				t.Fatalf("front matter end %d is past the document (%d)",
					doc.FrontMatter.ByteEnd, len(doc.Text))
			}
			if doc.BodyByte != doc.FrontMatter.ByteEnd {
				t.Fatalf("body offset %d does not follow the front matter (%d)",
					doc.BodyByte, doc.FrontMatter.ByteEnd)
			}
		}
		if doc.BodyByte < 0 || doc.BodyByte > len(doc.Text) {
			t.Fatalf("body offset %d out of range (text %d)", doc.BodyByte, len(doc.Text))
		}
		if doc.Text != "" && !strings.HasSuffix(doc.Text, doc.Body) {
			t.Fatal("body is not a suffix of the document text")
		}
		for _, s := range doc.Sections {
			if s.Span.ByteStart > s.Span.ByteEnd {
				t.Fatalf("section span is inverted: %+v", s.Span)
			}
			if s.Level < 0 || s.Level > 6 {
				t.Fatalf("section level out of range: %d", s.Level)
			}
		}
		// Diagnostics must be renderable and carry a code.
		for _, d := range doc.Diagnostics {
			if d.Code == "" || d.Fingerprint == "" {
				t.Fatalf("diagnostic without a code or fingerprint: %+v", d)
			}
		}
		// Bullets never panic either.
		for _, s := range doc.Sections {
			_ = Bullets(s.Content, 1)
		}
		// Valid UTF-8 in must stay valid UTF-8 out.
		if utf8.ValidString(input) && !utf8.ValidString(doc.Body) {
			t.Fatal("valid UTF-8 input produced invalid UTF-8 body")
		}
	})
}

func FuzzFrontMatter(f *testing.F) {
	f.Add("key: value")
	f.Add("list:\n  - a\n  - b")
	f.Add("nested:\n  a:\n    b: c")
	f.Add("block: |\n  line\n  line2")
	f.Add("a: [1, 2, {b: c}]")
	f.Add("dup: 1\ndup: 2")
	f.Add(strings.Repeat("a:\n  ", 50))
	f.Fuzz(func(t *testing.T, input string) {
		fields, keys, diags := parseFrontMatter("fuzz.md", input, 1)
		if fields == nil {
			t.Fatal("fields map must never be nil")
		}
		for _, k := range keys {
			if _, ok := fields[k]; !ok {
				t.Fatalf("key %q is listed but absent from the fields map", k)
			}
		}
		for _, d := range diags {
			if d.Code == "" {
				t.Fatal("diagnostic without a code")
			}
		}
		// Only the documented value types may appear.
		var check func(v any, depth int)
		check = func(v any, depth int) {
			if depth > 32 {
				t.Fatal("value nesting is unbounded")
			}
			switch t2 := v.(type) {
			case nil, string, bool, int64, float64:
			case []any:
				for _, item := range t2 {
					check(item, depth+1)
				}
			case map[string]any:
				for _, item := range t2 {
					check(item, depth+1)
				}
			default:
				t.Fatalf("unexpected front matter value type %T", v)
			}
		}
		for _, v := range fields {
			check(v, 0)
		}
	})
}
