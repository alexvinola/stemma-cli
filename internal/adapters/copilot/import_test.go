package copilot

import "testing"

// TestSplitApplyToHonoursBraceGroups pins the reading half of the applyTo
// ambiguity: the list separator and the separator inside a brace group are the
// same character, and splitting blindly turns one working pattern into two
// that match nothing.
func TestSplitApplyToHonoursBraceGroups(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"src/api/**", []string{"src/api/**"}},
		{"src/[{]name.{ts,tsx},lib/**", []string{"src/[{]name.{ts,tsx}", "lib/**"}},
		{"{src/[}],lib/[{]}/**,docs/**", []string{"{src/[}],lib/[{]}/**", "docs/**"}},
		{"src/api/**,src/handlers/**", []string{"src/api/**", "src/handlers/**"}},
		{"src/**/*.{ts,tsx}", []string{"src/**/*.{ts,tsx}"}},
		{
			"src/**/*.{ts,tsx},lib/**/*.go",
			[]string{"src/**/*.{ts,tsx}", "lib/**/*.go"},
		},
		{
			"{app,packages}/**/*.{css,scss}, ui/**",
			[]string{"{app,packages}/**/*.{css,scss}", "ui/**"},
		},
		// Nested groups keep their inner commas too.
		{"a/{b,{c,d}}/*.ts", []string{"a/{b,{c,d}}/*.ts"}},
		// An unclosed group is not a group: it is what the old, blind split
		// left behind. Splitting on every comma is what surfaces it to
		// validation as the corruption it is, rather than swallowing the rest
		// of the list into one pattern.
		{"src/**/*.{ts,tsx", []string{"src/**/*.{ts", "tsx"}},
		{"", nil},
		{"  ,  ", nil},
	}
	for _, c := range cases {
		got := splitApplyTo(c.in)
		if len(got) != len(c.want) {
			t.Errorf("splitApplyTo(%q) = %q, want %q", c.in, got, c.want)
			continue
		}
		for i := range c.want {
			if got[i] != c.want[i] {
				t.Errorf("splitApplyTo(%q) = %q, want %q", c.in, got, c.want)
				break
			}
		}
	}
}
