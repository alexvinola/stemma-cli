package workspace

import (
	"errors"
	"strings"
	"testing"
)

func TestNormalizeRelAccepts(t *testing.T) {
	cases := map[string]string{
		"CLAUDE.md":                 "CLAUDE.md",
		"./CLAUDE.md":               "CLAUDE.md",
		".github/instructions/a.md": ".github/instructions/a.md",
		"a/b/../c.md":               "a/c.md",
		"a//b.md":                   "a/b.md",
	}
	for in, want := range cases {
		got, err := NormalizeRel(in)
		if err != nil {
			t.Errorf("NormalizeRel(%q) returned %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("NormalizeRel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeRelRejects(t *testing.T) {
	cases := []string{
		"",
		"/etc/passwd",
		"../outside.md",
		"a/../../outside.md",
		"C:\\Windows\\system32",
		"c:/windows",
		`a\b.md`,
		"~/secrets",
		".",
		"..",
		"a\x00b",
		strings.Repeat("a", MaxPathLength+1),
	}
	for _, in := range cases {
		if got, err := NormalizeRel(in); err == nil {
			t.Errorf("NormalizeRel(%q) = %q, want an error", in, got)
		} else if !errors.Is(err, ErrPathEscape) {
			t.Errorf("NormalizeRel(%q) error = %v, want ErrPathEscape", in, err)
		}
	}
}

func TestJoinRel(t *testing.T) {
	got, err := JoinRel(".github/instructions", "api.instructions.md")
	if err != nil || got != ".github/instructions/api.instructions.md" {
		t.Fatalf("JoinRel = %q, %v", got, err)
	}
	if _, err := JoinRel("a", "../../b"); err == nil {
		t.Fatal("JoinRel must reject traversal")
	}
	if _, err := JoinRel("", ""); err == nil {
		t.Fatal("JoinRel must reject an empty result")
	}
}

func TestDir(t *testing.T) {
	if got := Dir("a/b/c.md"); got != "a/b" {
		t.Errorf("Dir = %q", got)
	}
	if got := Dir("c.md"); got != "" {
		t.Errorf("Dir at root = %q", got)
	}
}

func FuzzNormalizeRel(f *testing.F) {
	f.Add("a/b.md")
	f.Add("../x")
	f.Add("C:/x")
	f.Add("")
	f.Fuzz(func(t *testing.T, p string) {
		got, err := NormalizeRel(p)
		if err != nil {
			return
		}
		// A normalized path must never escape, be absolute, or keep traversal.
		if strings.HasPrefix(got, "/") || strings.HasPrefix(got, "../") ||
			got == ".." || strings.Contains(got, "\\") || strings.Contains(got, "\x00") {
			t.Fatalf("NormalizeRel(%q) returned unsafe path %q", p, got)
		}
		for _, seg := range strings.Split(got, "/") {
			if seg == "" || seg == "." || seg == ".." {
				t.Fatalf("NormalizeRel(%q) returned %q with bad segment %q", p, got, seg)
			}
		}
		// Normalization is idempotent.
		again, err := NormalizeRel(got)
		if err != nil || again != got {
			t.Fatalf("NormalizeRel is not idempotent for %q: %q -> %q (%v)", p, got, again, err)
		}
	})
}
