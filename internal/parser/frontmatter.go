// Package parser implements Stemma's deterministic Markdown and front matter
// parsing.
//
// The front matter parser deliberately supports only a small, safe subset of
// YAML. It never resolves tags, anchors, aliases or merge keys, so no input can
// cause it to construct arbitrary objects or expand exponentially.
package parser

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/alexvinola/stemma-cli/internal/diagnostics"
)

// Front matter limits.
const (
	MaxFrontMatterBytes = 64 << 10 // 64 KiB
	MaxFrontMatterLines = 2000
	MaxFrontMatterDepth = 8
	MaxFrontMatterKeys  = 500
)

// FrontMatter is a parsed front matter block.
type FrontMatter struct {
	// Raw is the exact text between the delimiters, excluding them.
	Raw string
	// Fields holds decoded values. Values are string, bool, int64, float64,
	// nil, []any or map[string]any.
	Fields map[string]any
	// Keys lists top-level keys in source order.
	Keys []string
	// StartLine and EndLine are 1-based lines of the opening and closing "---".
	StartLine int
	EndLine   int
	// ByteStart and ByteEnd delimit the whole block including delimiters.
	ByteStart int
	ByteEnd   int
}

// Has reports whether a key is present.
func (f *FrontMatter) Has(key string) bool {
	if f == nil {
		return false
	}
	_, ok := f.Fields[key]
	return ok
}

// String returns a string field. Numbers and booleans are rendered in their
// canonical textual form so that callers never have to type-switch.
func (f *FrontMatter) String(key string) (string, bool) {
	if f == nil {
		return "", false
	}
	v, ok := f.Fields[key]
	if !ok {
		return "", false
	}
	return scalarString(v)
}

func scalarString(v any) (string, bool) {
	switch t := v.(type) {
	case string:
		return t, true
	case bool:
		return strconv.FormatBool(t), true
	case int64:
		return strconv.FormatInt(t, 10), true
	case float64:
		return strconv.FormatFloat(t, 'g', -1, 64), true
	case nil:
		return "", true
	default:
		return "", false
	}
}

// StringList returns a field as a list of strings. A scalar becomes a
// single-element list; a comma-separated scalar is NOT split here, because
// splitting rules are provider-specific.
func (f *FrontMatter) StringList(key string) ([]string, bool) {
	if f == nil {
		return nil, false
	}
	v, ok := f.Fields[key]
	if !ok {
		return nil, false
	}
	switch t := v.(type) {
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			s, ok := scalarString(item)
			if !ok {
				return nil, false
			}
			out = append(out, s)
		}
		return out, true
	default:
		s, ok := scalarString(v)
		if !ok {
			return nil, false
		}
		if s == "" {
			return []string{}, true
		}
		return []string{s}, true
	}
}

// Bool returns a boolean field.
func (f *FrontMatter) Bool(key string) (bool, bool) {
	if f == nil {
		return false, false
	}
	v, ok := f.Fields[key]
	if !ok {
		return false, false
	}
	switch t := v.(type) {
	case bool:
		return t, true
	case string:
		switch strings.ToLower(strings.TrimSpace(t)) {
		case "true", "yes":
			return true, true
		case "false", "no":
			return false, true
		}
	}
	return false, false
}

// Except returns the fields with the given keys removed, for storing unknown
// keys as provider extensions.
func (f *FrontMatter) Except(known ...string) map[string]any {
	if f == nil || len(f.Fields) == 0 {
		return nil
	}
	skip := make(map[string]struct{}, len(known))
	for _, k := range known {
		skip[k] = struct{}{}
	}
	out := map[string]any{}
	for _, k := range f.Keys {
		if _, ok := skip[k]; ok {
			continue
		}
		out[k] = f.Fields[k]
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// UnknownKeys returns the sorted list of keys not in known.
func (f *FrontMatter) UnknownKeys(known ...string) []string {
	rest := f.Except(known...)
	if len(rest) == 0 {
		return nil
	}
	out := make([]string, 0, len(rest))
	for _, k := range f.Keys {
		if _, ok := rest[k]; ok {
			out = append(out, k)
		}
	}
	return out
}

// parseFrontMatter decodes the restricted YAML subset.
//
// path and startLine are used only to anchor diagnostics.
func parseFrontMatter(path, raw string, startLine int) (map[string]any, []string, []diagnostics.Diagnostic) {
	var diags []diagnostics.Diagnostic
	lines := splitLines(raw)
	if len(lines) > MaxFrontMatterLines {
		diags = append(diags, diagnostics.New(diagnostics.FrontMatterTooLarge, diagnostics.SeverityError,
			fmt.Sprintf("front matter has %d lines (limit %d)", len(lines), MaxFrontMatterLines)).
			WithPath(path).WithPosition(startLine, 1))
		return map[string]any{}, nil, diags
	}
	p := &fmParser{path: path, lines: lines, startLine: startLine}
	fields, keys := p.parseMapping(0, 0)
	p.checkTrailing()
	return fields, keys, append(diags, p.diags...)
}

type fmLine struct {
	text   string // without trailing newline
	number int    // 1-based within the whole document
	indent int
	body   string // text with indentation and trailing spaces removed
	blank  bool
}

type fmParser struct {
	path      string
	lines     []string
	startLine int
	pos       int
	keyCount  int
	diags     []diagnostics.Diagnostic
	stopped   bool
}

func (p *fmParser) lineAt(i int) (fmLine, bool) {
	if i < 0 || i >= len(p.lines) {
		return fmLine{}, false
	}
	raw := strings.TrimRight(p.lines[i], "\r")
	trimmed := strings.TrimLeft(raw, " ")
	indent := len(raw) - len(trimmed)
	body := stripComment(trimmed)
	return fmLine{
		text:   raw,
		number: p.startLine + 1 + i,
		indent: indent,
		body:   strings.TrimRight(body, " "),
		blank:  strings.TrimSpace(body) == "",
	}, true
}

func (p *fmParser) errf(line int, code diagnostics.Code, format string, args ...any) {
	p.diags = append(p.diags, diagnostics.New(code, diagnostics.SeverityError,
		fmt.Sprintf(format, args...)).WithPath(p.path).WithPosition(line, 1))
	p.stopped = true
}

func (p *fmParser) warnf(line int, code diagnostics.Code, format string, args ...any) {
	p.diags = append(p.diags, diagnostics.New(code, diagnostics.SeverityWarning,
		fmt.Sprintf(format, args...)).WithPath(p.path).WithPosition(line, 1))
}

func (p *fmParser) checkTrailing() {
	for p.pos < len(p.lines) && !p.stopped {
		l, _ := p.lineAt(p.pos)
		if !l.blank {
			p.errf(l.number, diagnostics.InvalidFrontMatter,
				"unexpected content in front matter: %q", sanitize(l.body))
			return
		}
		p.pos++
	}
}

// parseMapping parses a block mapping at the given indentation.
func (p *fmParser) parseMapping(indent, depth int) (map[string]any, []string) {
	fields := map[string]any{}
	var keys []string
	if depth > MaxFrontMatterDepth {
		l, _ := p.lineAt(p.pos)
		p.errf(l.number, diagnostics.InvalidFrontMatter,
			"front matter nesting exceeds %d levels", MaxFrontMatterDepth)
		return fields, keys
	}
	for p.pos < len(p.lines) && !p.stopped {
		l, ok := p.lineAt(p.pos)
		if !ok {
			break
		}
		if l.blank {
			p.pos++
			continue
		}
		if l.indent < indent {
			break
		}
		if l.indent > indent {
			p.errf(l.number, diagnostics.InvalidFrontMatter,
				"unexpected indentation in front matter at %q", sanitize(l.body))
			break
		}
		if strings.HasPrefix(l.body, "- ") || l.body == "-" {
			p.errf(l.number, diagnostics.InvalidFrontMatter,
				"list item found where a \"key: value\" pair was expected")
			break
		}
		if unsafeConstruct(l.body) {
			p.diags = append(p.diags, diagnostics.New(diagnostics.UnsafeYAMLConstruct, diagnostics.SeverityError,
				fmt.Sprintf("front matter uses an unsupported YAML construct: %q", sanitize(l.body))).
				WithPath(p.path).WithPosition(l.number, 1).
				WithDetail("Stemma refuses YAML tags, anchors, aliases, merge keys and multi-document streams.").
				WithSuggestion("Rewrite the value as a plain scalar, list or mapping."))
			p.stopped = true
			break
		}
		key, rest, ok := splitKey(l.body)
		if !ok {
			p.errf(l.number, diagnostics.InvalidFrontMatter,
				"expected \"key: value\" but found %q", sanitize(l.body))
			break
		}
		p.keyCount++
		if p.keyCount > MaxFrontMatterKeys {
			p.errf(l.number, diagnostics.FrontMatterTooLarge,
				"front matter declares more than %d keys", MaxFrontMatterKeys)
			break
		}
		p.pos++
		var value any
		switch {
		case rest == "":
			value = p.parseNestedValue(l.indent, depth)
		case rest == "|" || rest == "|-" || rest == "|+" || rest == ">" || rest == ">-" || rest == ">+":
			value = p.parseBlockScalar(rest, l.indent)
		default:
			value = decodeScalar(rest)
		}
		if _, dup := fields[key]; dup {
			p.warnf(l.number, diagnostics.InvalidFrontMatter,
				"duplicate front matter key %q; the last value wins", sanitize(key))
		} else {
			keys = append(keys, key)
		}
		fields[key] = value
	}
	return fields, keys
}

// parseNestedValue handles a key with no inline value: a nested mapping, a
// nested sequence, or an empty value.
func (p *fmParser) parseNestedValue(parentIndent, depth int) any {
	next := p.pos
	for next < len(p.lines) {
		l, _ := p.lineAt(next)
		if l.blank {
			next++
			continue
		}
		break
	}
	l, ok := p.lineAt(next)
	if !ok || l.indent <= parentIndent {
		return nil
	}
	p.pos = next
	if strings.HasPrefix(l.body, "- ") || l.body == "-" {
		return p.parseSequence(l.indent, depth+1)
	}
	m, _ := p.parseMapping(l.indent, depth+1)
	return m
}

func (p *fmParser) parseSequence(indent, depth int) []any {
	out := []any{}
	if depth > MaxFrontMatterDepth {
		l, _ := p.lineAt(p.pos)
		p.errf(l.number, diagnostics.InvalidFrontMatter,
			"front matter nesting exceeds %d levels", MaxFrontMatterDepth)
		return out
	}
	for p.pos < len(p.lines) && !p.stopped {
		l, ok := p.lineAt(p.pos)
		if !ok {
			break
		}
		if l.blank {
			p.pos++
			continue
		}
		if l.indent != indent || (!strings.HasPrefix(l.body, "- ") && l.body != "-") {
			break
		}
		item := strings.TrimSpace(strings.TrimPrefix(l.body, "-"))
		p.pos++
		if item == "" {
			out = append(out, p.parseNestedValue(l.indent, depth))
			continue
		}
		if unsafeConstruct(item) {
			p.diags = append(p.diags, diagnostics.New(diagnostics.UnsafeYAMLConstruct, diagnostics.SeverityError,
				fmt.Sprintf("front matter uses an unsupported YAML construct: %q", sanitize(item))).
				WithPath(p.path).WithPosition(l.number, 1))
			p.stopped = true
			break
		}
		// A "- key: value" item starts an inline mapping.
		if key, rest, ok := splitKey(item); ok && rest != "" {
			out = append(out, map[string]any{key: decodeScalar(rest)})
			continue
		}
		out = append(out, decodeScalar(item))
	}
	return out
}

func (p *fmParser) parseBlockScalar(style string, parentIndent int) string {
	folded := strings.HasPrefix(style, ">")
	chomp := strings.TrimLeft(style, "|>")
	var collected []string
	blockIndent := -1
	for p.pos < len(p.lines) {
		raw := strings.TrimRight(p.lines[p.pos], "\r")
		trimmed := strings.TrimLeft(raw, " ")
		indent := len(raw) - len(trimmed)
		if strings.TrimSpace(raw) == "" {
			collected = append(collected, "")
			p.pos++
			continue
		}
		if indent <= parentIndent {
			break
		}
		if blockIndent < 0 {
			blockIndent = indent
		}
		if indent < blockIndent {
			break
		}
		collected = append(collected, raw[blockIndent:])
		p.pos++
	}
	// Trim trailing blank lines produced by lookahead.
	for len(collected) > 0 && collected[len(collected)-1] == "" {
		collected = collected[:len(collected)-1]
	}
	var text string
	if folded {
		text = foldLines(collected)
	} else {
		text = strings.Join(collected, "\n")
	}
	switch chomp {
	case "-":
		return text
	case "+":
		return text + "\n"
	default:
		if text == "" {
			return ""
		}
		return text + "\n"
	}
}

func foldLines(lines []string) string {
	var out []string
	var current []string
	flush := func() {
		if len(current) > 0 {
			out = append(out, strings.Join(current, " "))
			current = nil
		}
	}
	for _, l := range lines {
		if l == "" {
			flush()
			out = append(out, "")
			continue
		}
		current = append(current, l)
	}
	flush()
	return strings.Join(out, "\n")
}

// splitKey splits "key: value" honouring quoted keys.
func splitKey(s string) (key, value string, ok bool) {
	if s == "" {
		return "", "", false
	}
	if s[0] == '"' || s[0] == '\'' {
		quote := s[0]
		end := strings.IndexByte(s[1:], quote)
		if end < 0 {
			return "", "", false
		}
		key = s[1 : 1+end]
		rest := strings.TrimSpace(s[2+end:])
		if !strings.HasPrefix(rest, ":") {
			return "", "", false
		}
		return key, strings.TrimSpace(rest[1:]), true
	}
	i := strings.IndexByte(s, ':')
	if i <= 0 {
		return "", "", false
	}
	key = strings.TrimSpace(s[:i])
	if key == "" || !validKey(key) {
		return "", "", false
	}
	rest := s[i+1:]
	if rest != "" && rest[0] != ' ' && rest[0] != '\t' {
		// "a:b" is not a mapping in this subset; treat it as an error so the
		// value is never silently reinterpreted.
		return "", "", false
	}
	return key, strings.TrimSpace(rest), true
}

func validKey(k string) bool {
	if len(k) > 200 {
		return false
	}
	for _, r := range k {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '_' || r == '-' || r == '.' || r == '/' || r == '$':
		default:
			return false
		}
	}
	return true
}

// unsafeConstruct detects YAML features Stemma refuses to interpret.
func unsafeConstruct(s string) bool {
	t := strings.TrimSpace(s)
	if t == "" {
		return false
	}
	if strings.HasPrefix(t, "<<:") {
		return true
	}
	if t == "---" || t == "..." {
		return true
	}
	// Look for tags/anchors/aliases in value position.
	if i := strings.IndexByte(t, ':'); i >= 0 {
		v := strings.TrimSpace(t[i+1:])
		if strings.HasPrefix(v, "!") || strings.HasPrefix(v, "&") || strings.HasPrefix(v, "*") {
			return true
		}
	}
	if strings.HasPrefix(t, "!") || strings.HasPrefix(t, "&") || strings.HasPrefix(t, "*") {
		return true
	}
	return false
}

// stripComment removes a trailing "# comment" that is not inside quotes.
func stripComment(s string) string {
	inSingle, inDouble := false, false
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
		case '"':
			if !inSingle {
				inDouble = !inDouble
			}
		case '#':
			if !inSingle && !inDouble && (i == 0 || s[i-1] == ' ' || s[i-1] == '\t') {
				return s[:i]
			}
		}
	}
	return s
}

// decodeScalar converts a scalar token into a typed value.
//
// Quoted scalars are always strings; unquoted scalars are typed only when they
// match an unambiguous literal form.
func decodeScalar(s string) any {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return unescapeDouble(s[1 : len(s)-1])
	}
	if len(s) >= 2 && s[0] == '\'' && s[len(s)-1] == '\'' {
		return strings.ReplaceAll(s[1:len(s)-1], "''", "'")
	}
	if strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]") {
		return decodeFlowSequence(s[1 : len(s)-1])
	}
	if strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}") {
		return decodeFlowMapping(s[1 : len(s)-1])
	}
	switch strings.ToLower(s) {
	case "true":
		return true
	case "false":
		return false
	case "null", "~":
		return nil
	}
	if i, err := strconv.ParseInt(s, 10, 64); err == nil && isPlainInt(s) {
		return i
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil && isPlainFloat(s) {
		return f
	}
	return s
}

func isPlainInt(s string) bool {
	t := strings.TrimPrefix(s, "-")
	if t == "" {
		return false
	}
	for _, r := range t {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func isPlainFloat(s string) bool {
	t := strings.TrimPrefix(s, "-")
	dot := false
	digits := false
	for _, r := range t {
		switch {
		case r >= '0' && r <= '9':
			digits = true
		case r == '.':
			if dot {
				return false
			}
			dot = true
		default:
			return false
		}
	}
	return dot && digits
}

func decodeFlowSequence(inner string) []any {
	out := []any{}
	for _, part := range splitFlow(inner) {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out = append(out, decodeScalar(part))
	}
	return out
}

func decodeFlowMapping(inner string) map[string]any {
	out := map[string]any{}
	for _, part := range splitFlow(inner) {
		key, value, ok := splitKey(strings.TrimSpace(part))
		if !ok {
			continue
		}
		out[key] = decodeScalar(value)
	}
	return out
}

// splitFlow splits on commas that are not inside quotes or nested brackets.
func splitFlow(s string) []string {
	var out []string
	depth := 0
	inSingle, inDouble := false, false
	start := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
		case '"':
			if !inSingle {
				inDouble = !inDouble
			}
		case '[', '{':
			if !inSingle && !inDouble {
				depth++
			}
		case ']', '}':
			if !inSingle && !inDouble && depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 && !inSingle && !inDouble {
				out = append(out, s[start:i])
				start = i + 1
			}
		}
	}
	out = append(out, s[start:])
	return out
}

func unescapeDouble(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i == len(s)-1 {
			b.WriteByte(s[i])
			continue
		}
		i++
		switch s[i] {
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		case 'r':
			b.WriteByte('\r')
		case '"':
			b.WriteByte('"')
		case '\\':
			b.WriteByte('\\')
		default:
			b.WriteByte('\\')
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// sanitize renders untrusted text safely inside a diagnostic message.
func sanitize(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			fmt.Fprintf(&b, "\\x%02x", r)
			continue
		}
		b.WriteRune(r)
		if b.Len() > 120 {
			b.WriteString("…")
			break
		}
	}
	return b.String()
}
