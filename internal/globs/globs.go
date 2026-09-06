// Package globs implements the deterministic glob dialect Stemma uses for
// path-scoped activation.
//
// The dialect is intentionally small and provider-neutral:
//
//	?      matches one character other than '/'
//	*      matches zero or more characters other than '/'
//	**     matches zero or more path segments (only as a whole segment)
//	[abc]  matches one character from a set, with ranges and a leading '!' or
//	       '^' negation; '/' can never be matched by a class
//	{a,b}  expands to the alternatives 'a' and 'b'; groups nest and combine
//
// Patterns are always repository-relative and always use forward slashes.
//
// Brace groups are expanded during [Normalize]. This removes the group commas
// before projecting to a comma-separated list such as Copilot's applyTo.
// Literal commas and braces in character classes are preserved. Expansion is
// bounded by [MaxExpansions]; [Validate] rejects patterns beyond the bound
// rather than accepting a partial expansion.
//
// A group needs a top-level comma to be a group: '{a}' is the literal text
// "{a}", matching the shell and minimatch. Ranges such as '{1..3}' are not
// supported and stay literal.
package globs

import (
	"errors"
	"fmt"
	"strings"
)

// ErrInvalid is returned for syntactically invalid patterns.
var ErrInvalid = errors.New("invalid glob pattern")

// ErrTooManyExpansions is returned by [Expand] when a pattern's brace groups
// would produce more than [MaxExpansions] patterns, or nest deeper than the
// parser accepts. Validate rejects these patterns because it cannot check
// every alternative within the bound.
var ErrTooManyExpansions = errors.New("brace expansion exceeds the supported bound")

const (
	maxPatternLength = 1024

	// MaxExpansions bounds brace expansion. Nested groups grow
	// combinatorially, so the bound is what keeps expansion cheap on hostile
	// input. This is a Stemma resource limit.
	MaxExpansions = 1000

	// maxBraceDepth bounds brace nesting, which bounds parser recursion.
	maxBraceDepth = 32
)

// Validate reports whether a pattern is usable. It returns a human-readable
// reason when the pattern is rejected.
//
// Brace groups are validated through their expansions, so a group can never
// smuggle in a construct — an absolute path, a '..' segment, a partial '**'
// segment — that a plain pattern is not allowed to contain.
func Validate(pattern string) error {
	if err := validateText(pattern); err != nil {
		return err
	}
	candidates, err := Expand(pattern)
	if err != nil {
		// Sampling one alternative cannot establish that the others are safe.
		// Refuse the pattern rather than validate an incomplete expansion.
		return fmt.Errorf("%w: %w; split the pattern into smaller groups", ErrInvalid, err)
	}
	for _, c := range candidates {
		if err := validateShape(c); err != nil {
			return err
		}
	}
	return nil
}

// validateText checks the properties of the pattern as written, before any
// brace expansion.
func validateText(pattern string) error {
	if pattern == "" {
		return wrap("pattern is empty")
	}
	if len(pattern) > maxPatternLength {
		return wrap("pattern is longer than 1024 bytes")
	}
	if strings.ContainsRune(pattern, '\\') {
		return wrap("pattern contains a backslash; use forward slashes")
	}
	if strings.ContainsRune(pattern, 0) {
		return wrap("pattern contains a NUL byte")
	}
	depth := 0
	for i := 0; i < len(pattern); i++ {
		if pattern[i] == '[' {
			end, ok := ClassEnd(pattern, i)
			if !ok {
				return wrap("unterminated character class '['")
			}
			i = end
			continue
		}
		switch pattern[i] {
		case '{':
			depth++
		case '}':
			if depth > 0 {
				depth--
			}
		}
	}
	if depth != 0 {
		return wrap("unterminated brace group '{'")
	}
	return nil
}

// validateShape checks a single expanded pattern.
func validateShape(pattern string) error {
	if pattern == "" {
		return wrap("pattern is empty")
	}
	if strings.HasPrefix(pattern, "/") {
		return wrap("pattern is absolute; patterns are repository-relative")
	}
	for _, seg := range strings.Split(pattern, "/") {
		if seg == ".." {
			return wrap("pattern escapes the repository root with '..'")
		}
		if strings.Contains(seg, "**") && seg != "**" {
			return wrap("'**' must occupy a whole path segment")
		}
	}
	// Validate character classes.
	for i := 0; i < len(pattern); i++ {
		if pattern[i] != '[' {
			continue
		}
		j := i + 1
		if j < len(pattern) && (pattern[j] == '!' || pattern[j] == '^') {
			j++
		}
		if j < len(pattern) && pattern[j] == ']' {
			j++
		}
		for j < len(pattern) && pattern[j] != ']' {
			j++
		}
		if j >= len(pattern) {
			return wrap("unterminated character class '['")
		}
		i = j
	}
	return nil
}

func wrap(reason string) error {
	return errors.New(ErrInvalid.Error() + ": " + reason)
}

// ClassEnd returns the closing bracket of a character class at start. A
// leading ']' (after optional negation) is a member, not the closing bracket.
// Callers scanning glob syntax use this to leave class members uninterpreted.
func ClassEnd(pattern string, start int) (int, bool) {
	if start < 0 || start >= len(pattern) || pattern[start] != '[' {
		return 0, false
	}
	i := start + 1
	if i < len(pattern) && (pattern[i] == '!' || pattern[i] == '^') {
		i++
	}
	if i < len(pattern) && pattern[i] == ']' {
		i++
	}
	for i < len(pattern) && pattern[i] != ']' {
		i++
	}
	return i, i < len(pattern)
}

// Expand returns the brace expansion of a pattern, in a deterministic order:
// the leftmost group varies slowest, exactly as the shell orders it.
// Duplicate expansions are removed.
//
// A pattern without brace groups expands to itself. A pattern whose expansion
// would exceed [MaxExpansions] returns [ErrTooManyExpansions] and no results:
// Normalize keeps the input intact on failure; Validate rejects it before
// import or compilation, so no partial expansion becomes an accepted scope.
func Expand(pattern string) ([]string, error) {
	if !strings.ContainsRune(pattern, '{') {
		return []string{pattern}, nil
	}
	sc := scanBraces(pattern)
	out, _, err := expandSeq(sc, 0, 0)
	if err != nil {
		return nil, err
	}
	return dedupe(out), nil
}

// ExpandAll expands every pattern in order. Patterns that exceed the expansion
// bound are kept verbatim in expanded and are also listed in unexpanded, so a
// caller holding a diagnostic bag can report them.
func ExpandAll(patterns []string) (expanded, unexpanded []string) {
	expanded = make([]string, 0, len(patterns))
	for _, p := range patterns {
		exp, err := Expand(p)
		if err != nil {
			expanded = append(expanded, p)
			unexpanded = append(unexpanded, p)
			continue
		}
		expanded = append(expanded, exp...)
	}
	return expanded, unexpanded
}

// braceScan is the result of one linear pass over a pattern: for every '{',
// where its '}' is and whether the two enclose a top-level comma.
//
// The pass exists for speed, not convenience. Deciding those two facts by
// parsing on demand means every unmatched or comma-less '{' is re-parsed once
// per enclosing brace, which is exponential: "{{{{…{" with thirty braces and
// no closer used to run for hours. With the table each decision is a lookup
// and the parse is linear.
type braceScan struct {
	s        string
	match    []int  // index of the '}' closing the '{' at this index, or -1
	hasComma []bool // whether that group holds a comma at its own level
}

func scanBraces(s string) *braceScan {
	sc := &braceScan{s: s, match: make([]int, len(s)), hasComma: make([]bool, len(s))}
	for i := range sc.match {
		sc.match[i] = -1
	}
	var open []int
	for i := 0; i < len(s); i++ {
		if end, ok := ClassEnd(s, i); ok {
			i = end
			continue
		}
		switch s[i] {
		case '{':
			open = append(open, i)
		case '}':
			if n := len(open); n > 0 {
				sc.match[open[n-1]] = i
				open = open[:n-1]
			}
		case ',':
			if n := len(open); n > 0 {
				sc.hasComma[open[n-1]] = true
			}
		}
	}
	return sc
}

// isGroup reports whether the '{' at i opens a real brace group: it must be
// closed, and it must hold a comma of its own. "{a}" is literal text, as in
// the shell.
func (sc *braceScan) isGroup(i int) bool {
	return sc.match[i] >= 0 && sc.hasComma[i]
}

// expandSeq expands the sequence of literals and groups starting at s[i].
//
// At depth 0 it consumes the rest of the string. Inside a group it stops at
// the ',' or '}' that ends the current alternative and returns that index.
func expandSeq(sc *braceScan, i, depth int) ([]string, int, error) {
	if depth > maxBraceDepth {
		return nil, 0, ErrTooManyExpansions
	}
	s := sc.s
	results := []string{""}
	// openLiteral counts the '{' characters this alternative has emitted as
	// literal text. Their closing '}' is literal too, and must not be mistaken
	// for the end of the enclosing group: in "{,{},}" the middle "{}" is
	// literal, and reading its '}' as the outer group's would cut the group
	// short and silently change the pattern.
	openLiteral := 0
	var lit strings.Builder
	flush := func() {
		if lit.Len() == 0 {
			return
		}
		suffix := lit.String()
		for k := range results {
			results[k] += suffix
		}
		lit.Reset()
	}
	for i < len(s) {
		if end, ok := ClassEnd(s, i); ok {
			lit.WriteString(s[i : end+1])
			i = end + 1
			continue
		}
		c := s[i]
		if depth > 0 && c == '}' && openLiteral > 0 {
			openLiteral--
			lit.WriteByte('}')
			i++
			continue
		}
		if depth > 0 && (c == ',' || c == '}') {
			flush()
			return results, i, nil
		}
		if c != '{' || !sc.isGroup(i) {
			// Not a group: an unclosed brace, or one holding no comma. The
			// character is literal, and any real group nested inside is still
			// expanded as the scan walks past it.
			if c == '{' {
				openLiteral++
			}
			lit.WriteByte(c)
			i++
			continue
		}
		alts, end, err := expandGroup(sc, i, depth)
		if err != nil {
			return nil, 0, err
		}
		flush()
		if len(results)*len(alts) > MaxExpansions {
			return nil, 0, ErrTooManyExpansions
		}
		combined := make([]string, 0, len(results)*len(alts))
		for _, r := range results {
			for _, a := range alts {
				combined = append(combined, r+a)
			}
		}
		results = combined
		i = end
	}
	flush()
	return results, i, nil
}

// expandGroup expands the brace group at s[i], which sc.isGroup has already
// confirmed. It returns the alternatives and the index just past the closing
// '}'.
func expandGroup(sc *braceScan, i, depth int) (alts []string, next int, err error) {
	closing := sc.match[i]
	j := i + 1
	for {
		part, end, err := expandSeq(sc, j, depth+1)
		if err != nil {
			return nil, 0, err
		}
		alts = append(alts, part...)
		if len(alts) > MaxExpansions {
			return nil, 0, ErrTooManyExpansions
		}
		if end < closing && sc.s[end] == ',' {
			j = end + 1
			continue
		}
		return alts, closing + 1, nil
	}
}

func dedupe(in []string) []string {
	out := make([]string, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

// Match reports whether a repository-relative path matches the pattern.
// A pattern with brace groups matches when any of its expansions matches.
// Invalid patterns never match.
func Match(pattern, path string) bool {
	if Validate(pattern) != nil {
		return false
	}
	path = strings.TrimPrefix(path, "./")
	seg := strings.Split(path, "/")
	candidates, err := Expand(pattern)
	if err != nil {
		// Too large to expand: fall back to the pattern as written rather
		// than claiming a match Stemma cannot justify.
		candidates = []string{pattern}
	}
	for _, c := range candidates {
		if matchSegments(strings.Split(c, "/"), seg) {
			return true
		}
	}
	return false
}

// MatchAny reports whether any pattern matches the path.
func MatchAny(patterns []string, path string) bool {
	for _, p := range patterns {
		if Match(p, path) {
			return true
		}
	}
	return false
}

func matchSegments(pat, seg []string) bool {
	switch {
	case len(pat) == 0:
		return len(seg) == 0
	case pat[0] == "**":
		// '**' matches zero or more segments.
		for i := 0; i <= len(seg); i++ {
			if matchSegments(pat[1:], seg[i:]) {
				return true
			}
		}
		return false
	case len(seg) == 0:
		return false
	case matchSegment(pat[0], seg[0]):
		return matchSegments(pat[1:], seg[1:])
	default:
		return false
	}
}

// matchSegment matches a single path segment against a single pattern segment.
func matchSegment(pat, s string) bool {
	pr := []rune(pat)
	sr := []rune(s)
	return matchRunes(pr, sr)
}

func matchRunes(pat, s []rune) bool {
	for len(pat) > 0 {
		switch pat[0] {
		case '*':
			// Collapse consecutive stars.
			for len(pat) > 0 && pat[0] == '*' {
				pat = pat[1:]
			}
			if len(pat) == 0 {
				return true
			}
			for i := 0; i <= len(s); i++ {
				if matchRunes(pat, s[i:]) {
					return true
				}
			}
			return false
		case '?':
			if len(s) == 0 {
				return false
			}
			pat, s = pat[1:], s[1:]
		case '[':
			if len(s) == 0 {
				return false
			}
			rest, ok := matchClass(pat, s[0])
			if !ok {
				return false
			}
			pat, s = rest, s[1:]
		default:
			if len(s) == 0 || s[0] != pat[0] {
				return false
			}
			pat, s = pat[1:], s[1:]
		}
	}
	return len(s) == 0
}

// matchClass consumes a bracket expression at the head of pat and reports
// whether r is a member. It assumes pat[0] == '['.
func matchClass(pat []rune, r rune) ([]rune, bool) {
	i := 1
	negated := false
	if i < len(pat) && (pat[i] == '!' || pat[i] == '^') {
		negated = true
		i++
	}
	matched := false
	first := true
	for i < len(pat) && (pat[i] != ']' || first) {
		first = false
		if i+2 < len(pat) && pat[i+1] == '-' && pat[i+2] != ']' {
			lo, hi := pat[i], pat[i+2]
			if lo <= r && r <= hi {
				matched = true
			}
			i += 3
			continue
		}
		if pat[i] == r {
			matched = true
		}
		i++
	}
	if i >= len(pat) {
		return nil, false // unterminated; Validate rejects these earlier
	}
	if r == '/' {
		return pat[i+1:], false
	}
	return pat[i+1:], matched != negated
}

// LiteralPrefix returns the longest leading directory path that contains no
// wildcard characters. It never returns a partial segment.
//
//	"src/api/**"        -> "src/api"
//	"src/api/*.ts"      -> "src/api"
//	"**/*.ts"           -> ""
//	"src/api*/x.ts"     -> "src"
//
// A brace is treated as a wildcard even where it would expand to a single
// literal: under-claiming a prefix only makes a caller more cautious, while
// over-claiming one would silently widen a scope.
func LiteralPrefix(pattern string) string {
	segs := strings.Split(pattern, "/")
	var out []string
	for _, seg := range segs {
		if strings.ContainsAny(seg, "*?[{}") {
			break
		}
		out = append(out, seg)
	}
	// The final segment of a pattern is a file component unless the pattern
	// ends with a separator, so drop it when it is the whole pattern.
	if len(out) == len(segs) && len(out) > 0 {
		out = out[:len(out)-1]
	}
	return strings.Join(out, "/")
}

// DirectoryScope derives a single concrete directory that safely contains all
// of the given include patterns, or returns ok=false when no such directory
// can be derived without inventing one.
//
// A directory is derivable only when every pattern shares the same non-empty
// literal directory prefix and every pattern is fully contained by it. Brace
// groups are expanded first, so "src/api/*.{ts,tsx}" still resolves to
// "src/api" instead of being refused for its braces.
func DirectoryScope(includes []string) (dir string, ok bool) {
	if len(includes) == 0 {
		return "", false
	}
	expanded, _ := ExpandAll(includes)
	for i, p := range expanded {
		if Validate(p) != nil {
			return "", false
		}
		prefix := LiteralPrefix(p)
		if prefix == "" {
			return "", false
		}
		if i == 0 {
			dir = prefix
			continue
		}
		if prefix != dir {
			return "", false
		}
	}
	return dir, dir != ""
}

// Normalize returns a canonical form of the pattern list: brace-expanded,
// trimmed, with duplicates removed, preserving first-seen order.
//
// Unexpandable patterns are kept as written so validation can reject them
// without silent truncation. Literal commas and character classes are retained.
func Normalize(patterns []string) []string {
	out := make([]string, 0, len(patterns))
	seen := make(map[string]struct{}, len(patterns))
	for _, raw := range patterns {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		expanded, err := Expand(raw)
		if err != nil {
			expanded = []string{raw}
		}
		for _, p := range expanded {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			p = strings.TrimPrefix(p, "./")
			if _, ok := seen[p]; ok {
				continue
			}
			seen[p] = struct{}{}
			out = append(out, p)
		}
	}
	return out
}
