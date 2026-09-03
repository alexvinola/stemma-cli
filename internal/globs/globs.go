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
//
// Patterns are always repository-relative and always use forward slashes.
// There is no brace expansion: braces are matched literally, because provider
// support for them is inconsistent and guessing would be unsafe.
package globs

import (
	"errors"
	"strings"
)

// ErrInvalid is returned for syntactically invalid patterns.
var ErrInvalid = errors.New("invalid glob pattern")

const maxPatternLength = 1024

// Validate reports whether a pattern is usable. It returns a human-readable
// reason when the pattern is rejected.
func Validate(pattern string) error {
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

// Match reports whether a repository-relative path matches the pattern.
// Invalid patterns never match.
func Match(pattern, path string) bool {
	if Validate(pattern) != nil {
		return false
	}
	path = strings.TrimPrefix(path, "./")
	return matchSegments(strings.Split(pattern, "/"), strings.Split(path, "/"))
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
func LiteralPrefix(pattern string) string {
	segs := strings.Split(pattern, "/")
	var out []string
	for _, seg := range segs {
		if strings.ContainsAny(seg, "*?[") {
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
// literal directory prefix and every pattern is fully contained by it.
func DirectoryScope(includes []string) (dir string, ok bool) {
	if len(includes) == 0 {
		return "", false
	}
	for i, p := range includes {
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

// Normalize returns a canonical form of the pattern list: trimmed, with
// duplicates removed, preserving first-seen order.
func Normalize(patterns []string) []string {
	out := make([]string, 0, len(patterns))
	seen := make(map[string]struct{}, len(patterns))
	for _, p := range patterns {
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
	return out
}
