package globs

import (
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

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
		// Brace groups expand, so both alternatives match.
		{"src/*.{ts,tsx}", "src/a.ts", true},
		{"src/*.{ts,tsx}", "src/a.tsx", true},
		{"src/*.{ts,tsx}", "src/a.go", false},
		{"src/*.{ts,tsx}", "src/a.{ts,tsx}", false},
		{"src/**/*.{ts,tsx}", "src/api/handlers/x.tsx", true},
		{"{src,lib}/**/*.go", "lib/deep/x.go", true},
		{"{src,lib}/**/*.go", "vendor/x.go", false},
		// A group without a top-level comma is literal text, as in the shell.
		{"src/{a}.ts", "src/{a}.ts", true},
		{"src/{a}.ts", "src/a.ts", false},
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
	invalid := []string{
		"", "/abs/path", "../escape", "a/../b", "src\\win", "a/**b", "src/[abc", "a\x00b",
		// An unterminated group is what a corrupted comma-separated list looks
		// like, so it is rejected rather than matched as literal text.
		"src/**/*.{ts",
		// A group must not be able to smuggle in what a plain pattern cannot.
		"{a,/abs}/x.ts", "src/{..,ok}/x.ts", "src/{**x,ok}/y.ts",
	}
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
		"{src,lib}/api":  "",
		"src/{a,b}/c.md": "src",
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
	// Brace groups are expanded first, so a group inside one segment no longer
	// hides a directory that every alternative shares.
	if dir, ok := DirectoryScope([]string{"src/api/*.{ts,tsx}"}); !ok || dir != "src/api" {
		t.Errorf("DirectoryScope with a brace group = %q %v, want \"src/api\" true", dir, ok)
	}
	if _, ok := DirectoryScope([]string{"{src,lib}/api/**"}); ok {
		t.Error("a group spanning two roots must not resolve to one directory")
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

func TestExpand(t *testing.T) {
	cases := []struct {
		pattern string
		want    []string
	}{
		{"src/**/*.ts", []string{"src/**/*.ts"}},
		{"src/**/*.{ts,tsx}", []string{"src/**/*.ts", "src/**/*.tsx"}},
		{"{src,lib}/**/*.{go,md}", []string{
			"src/**/*.go", "src/**/*.md", "lib/**/*.go", "lib/**/*.md",
		}},
		// Nested groups.
		{"a/{b,{c,d}}/x", []string{"a/b/x", "a/c/x", "a/d/x"}},
		// A group with no top-level comma is literal, and any group inside it
		// still expands.
		{"a/{b}/x", []string{"a/{b}/x"}},
		{"{a{b,c}}", []string{"{ab}", "{ac}"}},
		// Duplicate alternatives collapse.
		{"*.{ts,ts}", []string{"*.ts"}},
		// An unterminated brace is literal text; Validate is what rejects it.
		{"src/*.{ts", []string{"src/*.{ts"}},
		{"src/*.ts}", []string{"src/*.ts}"}},
	}
	for _, c := range cases {
		got, err := Expand(c.pattern)
		if err != nil {
			t.Errorf("Expand(%q) = %v", c.pattern, err)
			continue
		}
		if len(got) != len(c.want) {
			t.Errorf("Expand(%q) = %v, want %v", c.pattern, got, c.want)
			continue
		}
		for i := range c.want {
			if got[i] != c.want[i] {
				t.Errorf("Expand(%q) = %v, want %v", c.pattern, got, c.want)
				break
			}
		}
	}
}

// TestExpandIsBounded is the guard against the combinatorial blow-up: a
// pattern that would expand past the bound must be refused quickly, not
// expanded slowly and not truncated.
func TestExpandIsBounded(t *testing.T) {
	hostile := strings.Repeat("{a,b}", 200) // 2^200 expansions
	if _, err := Expand(hostile); !errors.Is(err, ErrTooManyExpansions) {
		t.Fatalf("Expand(hostile) error = %v, want ErrTooManyExpansions", err)
	}
	// Deep nesting is bounded too, and neither case may take the parser down.
	deep := strings.Repeat("{a,", 500) + "b" + strings.Repeat("}", 500)
	if _, err := Expand(deep); !errors.Is(err, ErrTooManyExpansions) {
		t.Fatalf("Expand(deep) error = %v, want ErrTooManyExpansions", err)
	}
	// Exactly at the bound is still expanded.
	atBound := "{" + strings.Repeat("a,", MaxExpansions-1) + "a}"
	got, err := Expand(atBound)
	if err != nil {
		t.Fatalf("Expand at the bound = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Expand at the bound returned %d patterns, want 1 after dedupe", len(got))
	}
}

// Normalize retains oversized input for validation; it never silently truncates it.
func TestUnexpandablePatternIsPreservedButRejected(t *testing.T) {
	hostile := "src/" + strings.Repeat("{a,b}", 200) + ".ts"
	if err := Validate(hostile); !errors.Is(err, ErrTooManyExpansions) {
		t.Fatalf("Validate(hostile) = %v, want ErrTooManyExpansions", err)
	}
	expanded, unexpanded := ExpandAll([]string{"a/*.{ts,tsx}", hostile})
	want := []string{"a/*.ts", "a/*.tsx", hostile}
	if len(expanded) != len(want) {
		t.Fatalf("ExpandAll = %v, want %v", expanded, want)
	}
	for i := range want {
		if expanded[i] != want[i] {
			t.Fatalf("ExpandAll = %v, want %v", expanded, want)
		}
	}
	if len(unexpanded) != 1 || unexpanded[0] != hostile {
		t.Fatalf("ExpandAll unexpanded = %v, want [%q]", unexpanded, hostile)
	}
}

// TestNormalizeExpandsBraces is the invariant the Copilot applyTo bug turned
// on: nothing that reaches the canonical model may carry a comma.
func TestNormalizeExpandsBraces(t *testing.T) {
	got := Normalize([]string{"src/**/*.{ts,tsx}", "lib/**/*.go", "src/**/*.ts"})
	want := []string{"src/**/*.ts", "src/**/*.tsx", "lib/**/*.go"}
	if len(got) != len(want) {
		t.Fatalf("Normalize = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Normalize = %v, want %v", got, want)
		}
	}
	for _, p := range got {
		if strings.ContainsAny(p, ",{}") {
			t.Errorf("normalized pattern %q still carries brace-list syntax", p)
		}
	}
}

// TestHostilePatternsAreLinear guards the property that makes the bound
// meaningful: refusing a pattern must be cheap. Deciding "is this a group?"
// by parsing on demand re-parses every unmatched brace once per enclosing
// brace, which turns a 34-character pattern into hours of work. Each of these
// used to hang; all of them must finish well inside the test timeout.
func TestHostilePatternsAreLinear(t *testing.T) {
	hostile := []string{
		strings.Repeat("{", 34) + "0",
		strings.Repeat("{", 500) + "0",
		strings.Repeat("{a", 400),
		strings.Repeat("{a,b", 300),
		strings.Repeat("{a{b{c", 200),
		"{" + strings.Repeat("{},", 300) + "}",
		strings.Repeat("{,", 400) + strings.Repeat("}", 400),
		strings.Repeat("{a,b}", 200),
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for _, p := range hostile {
			_ = Validate(p)
			_, _ = Expand(p)
			_ = Match(p, "a/b/c.ts")
			_ = Normalize([]string{p})
			_ = LiteralPrefix(p)
			_, _ = DirectoryScope([]string{p})
		}
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("a hostile brace pattern did not finish in 10s")
	}
}

func FuzzMatch(f *testing.F) {
	f.Add("**/*.ts", "src/a.ts")
	f.Add("[", "a")
	f.Add("a/**/b", "a/b")
	f.Add("", "")
	f.Add("src/**/*.{ts,tsx}", "src/a.tsx")
	f.Add("src/[{]name.{ts,tsx}", "src/{name.ts")
	f.Add("{src/[}],lib/[{]}/**", "lib/{/a.ts")
	f.Add("{a,{b,c}}/*.go", "b/x.go")
	f.Add("{a,b", "a")
	f.Add("}{", "a")
	f.Add(strings.Repeat("{a,b}", 40), "aaaa")
	f.Fuzz(func(t *testing.T, pattern, path string) {
		// Must never panic, whatever the input is.
		_ = Validate(pattern)
		_ = Match(pattern, path)
		_ = LiteralPrefix(pattern)
		_, _ = DirectoryScope([]string{pattern})
		_ = Normalize([]string{pattern})
		expanded, err := Expand(pattern)
		if err != nil {
			return
		}
		if len(expanded) > MaxExpansions {
			t.Fatalf("Expand(%q) returned %d patterns, over the bound", pattern, len(expanded))
		}
		// Expansion must never invent a construct the pattern could not
		// contain: a valid pattern may only expand into valid patterns.
		if Validate(pattern) != nil {
			return
		}
		for _, e := range expanded {
			if err := Validate(e); err != nil {
				t.Fatalf("Expand(%q) produced invalid pattern %q: %v", pattern, e, err)
			}
		}
	})
}

func TestBraceCharacterClasses(t *testing.T) {
	cases := []struct {
		pattern, path string
		want          bool
	}{
		{"src/[{]name.ts", "src/{name.ts", true},
		{"src/[}]name.ts", "src/}name.ts", true},
		{"src/[!{]name.ts", "src/{name.ts", false},
		{"src/[!{]name.ts", "src/aname.ts", true},
		{"src/[]{}]name.ts", "src/]name.ts", true},
		{"src/[{]name.{ts,tsx}", "src/{name.tsx", true},
		{"{src/[}],lib/[{]}/**", "lib/{/a.ts", true},
		{"{src/[}],lib/[{]}/**", "src/}/a.ts", true},
		{"{src/[}],lib/[{]}/**", "src/{/a.ts", false},
	}
	for _, tc := range cases {
		if err := Validate(tc.pattern); err != nil {
			t.Errorf("Validate(%q): %v", tc.pattern, err)
		}
		if got := Match(tc.pattern, tc.path); got != tc.want {
			t.Errorf("Match(%q, %q) = %v", tc.pattern, tc.path, got)
		}
	}
	got, err := Expand("src/[{,}]name.{ts,tsx}")
	if err != nil || strings.Join(got, "|") != "src/[{,}]name.ts|src/[{,}]name.tsx" {
		t.Fatalf("class members changed during expansion: %q, %v", got, err)
	}
}

func TestExpansionLimitNeverSamplesValidation(t *testing.T) {
	many := strings.Repeat("{a,b}", 11)
	for _, p := range []string{
		"src/" + many + "/{ok,..}/**",
		"{src,/absolute}/" + many,
		"src/" + many + "/{ok,**bad}",
		"src/" + many + "/safe",
	} {
		if err := Validate(p); !errors.Is(err, ErrTooManyExpansions) || !errors.Is(err, ErrInvalid) {
			t.Errorf("Validate(%q) = %v, want a blocking expansion bound", p, err)
		}
		if Match(p, p) {
			t.Errorf("rejected pattern matches literally: %q", p)
		}
	}
	// 10 x 10 x 10 alternatives are within both the count and byte limits.
	group := "{a,b,c,d,e,f,g,h,i,j}"
	atBound := "src/" + strings.Repeat(group, 3)
	if got, err := Expand(atBound); err != nil || len(got) != MaxExpansions {
		t.Fatalf("exact bound: count=%d, err=%v", len(got), err)
	}
	if err := Validate(atBound); err != nil {
		t.Fatal(err)
	}
}

func TestLiteralSubtree(t *testing.T) {
	cases := []struct {
		dir, pattern string
		inside       []string
		outside      []string
	}{
		{"src/api", "src/api/**", []string{"src/api/x.ts", "src/api/a/b.ts"}, []string{"src/apix/y.ts", "src/x.ts"}},
		{"app/[id]", "app/[[]id]/**", []string{"app/[id]/handler.ts"}, []string{"app/i/handler.ts", "app/d/handler.ts"}},
		{"app/[...slug]", "app/[[]...slug]/**", []string{"app/[...slug]/page.tsx"}, []string{"app/s/page.tsx", "app/./page.tsx"}},
		{"a/{b,c}", "a/[{]b,c[}]/**", []string{"a/{b,c}/x"}, []string{"a/b/x", "a/c/x"}},
		{"a/{b}", "a/[{]b[}]/**", []string{"a/{b}/x"}, []string{"a/b/x"}},
		{"q/a?b", "q/a[?]b/**", []string{"q/a?b/x"}, []string{"q/axb/x"}},
		{"s/*", "s/[*]/**", []string{"s/*/x"}, []string{"s/anything/x"}},
		{"s/**", "s/[*][*]/**", []string{"s/**/x"}, []string{"s/a/x", "s/x"}},
		{"odd/]x", "odd/]x/**", []string{"odd/]x/y"}, []string{"odd/x/y"}},
		{"odd/[!]", "odd/[[]!]/**", []string{"odd/[!]/y"}, []string{"odd/a/y", "odd/!/y"}},
		{"odd/!neg", "odd/!neg/**", []string{"odd/!neg/y"}, []string{"odd/neg/y"}},
	}
	for _, c := range cases {
		got := LiteralSubtree(c.dir)
		if got != c.pattern {
			t.Errorf("LiteralSubtree(%q) = %q, want %q", c.dir, got, c.pattern)
		}
		if err := Validate(got); err != nil {
			t.Errorf("LiteralSubtree(%q) = %q is invalid: %v", c.dir, got, err)
		}
		for _, p := range c.inside {
			if !Match(got, p) {
				t.Errorf("%q should match %q", got, p)
			}
		}
		for _, p := range c.outside {
			if Match(got, p) {
				t.Errorf("%q must not match %q", got, p)
			}
		}
		if dir, ok := LiteralSubtreeDir(got); !ok || dir != c.dir {
			t.Errorf("LiteralSubtreeDir(%q) = %q, %v; want %q", got, dir, ok, c.dir)
		}
	}
}

func TestLiteralSubtreeDirRejectsGlobs(t *testing.T) {
	for _, p := range []string{
		"src/api/*.ts", "src/*/**", "src/[a]pi/**", "src/[ab]/**", "src/[!a]/**", "src/{a,b}/**",
		"**", "/**", "src/**/x/**", "src/api", "./src/**", "src/../x/**", "a//b/**",
	} {
		if dir, ok := LiteralSubtreeDir(p); ok {
			t.Errorf("LiteralSubtreeDir(%q) = %q, want no literal directory", p, dir)
		}
	}
}

// FuzzLiteralSubtree checks the quoting invariant for arbitrary directory
// names: the quoted subtree matches the directory's own files and never a
// path obtained by replacing a special character.
func FuzzLiteralSubtree(f *testing.F) {
	for _, s := range []string{"app/[id]", "a/{b,c}", "q/a?b", "s/*", "x/[!]", "x/]", "plain/dir", "é/[ü]"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, dir string) {
		pattern := LiteralSubtree(dir)
		_, _ = LiteralSubtreeDir(pattern)
		_, _ = UnquoteLiteral(dir)
		if !literalDirCandidate(dir) || Validate(pattern) != nil {
			return
		}
		if !Match(pattern, dir+"/x") || !Match(pattern, dir+"/a/b") {
			t.Fatalf("%q does not match files in %q", pattern, dir)
		}
		if got, ok := LiteralSubtreeDir(pattern); !ok || got != dir {
			t.Fatalf("LiteralSubtreeDir(%q) = %q, %v; want %q", pattern, got, ok, dir)
		}
		sibling := strings.Map(func(r rune) rune {
			if strings.ContainsRune(literalSpecial, r) {
				return 'z'
			}
			return r
		}, dir)
		if sibling != dir && Match(pattern, sibling+"/x") {
			t.Fatalf("%q matches the glob-expanded sibling %q", pattern, sibling)
		}
	})
}

// literalDirCandidate accepts what a normalized repository directory can be.
func literalDirCandidate(dir string) bool {
	if dir == "" || !utf8.ValidString(dir) || strings.ContainsAny(dir, "\\\x00") {
		return false
	}
	for _, seg := range strings.Split(dir, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return false
		}
	}
	return true
}
