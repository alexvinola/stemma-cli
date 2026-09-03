package globs

import "testing"

func TestMatch(t *testing.T) {
	cases := []struct {
		pattern, path string
		want          bool
	}{
		{"**", "a.ts", true},
		{"**", "src/a.ts", true},
		{"**/*.ts", "a.ts", true},
		{"**/*.ts", "src/api/a.ts", true},
		{"**/*.ts", "src/api/a.tsx", false},
		{"src/**", "src/a.ts", true},
		{"src/**", "src/api/deep/a.ts", true},
		{"src/**", "lib/a.ts", false},
		{"src/*.ts", "src/a.ts", true},
		{"src/*.ts", "src/api/a.ts", false},
		{"src/api/**", "src/api/handlers/x.go", true},
		{"src/?.ts", "src/a.ts", true},
		{"src/?.ts", "src/ab.ts", false},
		{"src/[ab].ts", "src/a.ts", true},
		{"src/[ab].ts", "src/c.ts", false},
		{"src/[!ab].ts", "src/c.ts", true},
		{"src/[!ab].ts", "src/a.ts", false},
		{"*", "a.ts", true},
		{"*", "src/a.ts", false},
		{"docs/*.md", "docs/x.md", true},
		{"a/**/b.ts", "a/b.ts", true},
		{"a/**/b.ts", "a/x/y/b.ts", true},
		// Braces are matched literally; Stemma never expands them.
		{"src/*.{ts,tsx}", "src/a.ts", false},
		{"src/*.{ts,tsx}", "src/a.{ts,tsx}", true},
	}
	for _, c := range cases {
		if got := Match(c.pattern, c.path); got != c.want {
			t.Errorf("Match(%q, %q) = %v, want %v", c.pattern, c.path, got, c.want)
		}
	}
}

func TestValidate(t *testing.T) {
	valid := []string{"**", "src/**", "**/*.ts", "src/[ab].go", "a/b/c.md", "src/*.{ts,tsx}"}
	for _, p := range valid {
		if err := Validate(p); err != nil {
			t.Errorf("Validate(%q) = %v, want nil", p, err)
		}
	}
	invalid := []string{"", "/abs/path", "../escape", "a/../b", "src\\win", "a/**b", "src/[abc", "a\x00b"}
	for _, p := range invalid {
		if err := Validate(p); err == nil {
			t.Errorf("Validate(%q) = nil, want an error", p)
		}
	}
}

func TestInvalidPatternNeverMatches(t *testing.T) {
	if Match("../**", "../etc/passwd") {
		t.Error("an invalid pattern must never match")
	}
}

func TestLiteralPrefix(t *testing.T) {
	cases := map[string]string{
		"src/api/**":     "src/api",
		"src/api/*.ts":   "src/api",
		"**/*.ts":        "",
		"src/api*/x.ts":  "src",
		"a/b/c.md":       "a/b",
		"README.md":      "",
		"src/**/*.{a,b}": "src",
	}
	for pattern, want := range cases {
		if got := LiteralPrefix(pattern); got != want {
			t.Errorf("LiteralPrefix(%q) = %q, want %q", pattern, got, want)
		}
	}
}

func TestDirectoryScope(t *testing.T) {
	if dir, ok := DirectoryScope([]string{"src/api/**"}); !ok || dir != "src/api" {
		t.Errorf("DirectoryScope single = %q %v", dir, ok)
	}
	if dir, ok := DirectoryScope([]string{"src/api/**", "src/api/*.ts"}); !ok || dir != "src/api" {
		t.Errorf("DirectoryScope shared prefix = %q %v", dir, ok)
	}
	if _, ok := DirectoryScope([]string{"src/api/**", "src/lib/**"}); ok {
		t.Error("different prefixes must not resolve to one directory")
	}
	if _, ok := DirectoryScope([]string{"**/*.ts"}); ok {
		t.Error("a pattern with no literal prefix must not resolve to a directory")
	}
	if _, ok := DirectoryScope(nil); ok {
		t.Error("no patterns must not resolve to a directory")
	}
}

func TestNormalize(t *testing.T) {
	got := Normalize([]string{" src/** ", "src/**", "", "./docs/*.md"})
	want := []string{"src/**", "docs/*.md"}
	if len(got) != len(want) {
		t.Fatalf("Normalize = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Normalize = %v, want %v", got, want)
		}
	}
}

func FuzzMatch(f *testing.F) {
	f.Add("**/*.ts", "src/a.ts")
	f.Add("[", "a")
	f.Add("a/**/b", "a/b")
	f.Add("", "")
	f.Fuzz(func(t *testing.T, pattern, path string) {
		// Must never panic, whatever the input is.
		_ = Validate(pattern)
		_ = Match(pattern, path)
		_ = LiteralPrefix(pattern)
		_, _ = DirectoryScope([]string{pattern})
	})
}
