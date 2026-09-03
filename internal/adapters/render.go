package adapters

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/alexvinola/stemma-cli/internal/canonical"
)

// KV is an ordered front matter entry.
type KV struct {
	Key   string
	Value any
}

// RenderFrontMatter renders a YAML front matter block in the restricted subset
// Stemma parses. Keys are emitted in the given order; nested map keys are
// sorted so output never depends on map iteration order.
func RenderFrontMatter(entries []KV) string {
	if len(entries) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("---\n")
	for _, e := range entries {
		writeYAMLEntry(&b, e.Key, e.Value, 0)
	}
	b.WriteString("---\n\n")
	return b.String()
}

func writeYAMLEntry(b *strings.Builder, key string, value any, indent int) {
	pad := strings.Repeat("  ", indent)
	switch v := value.(type) {
	case []string:
		if len(v) == 0 {
			fmt.Fprintf(b, "%s%s: []\n", pad, key)
			return
		}
		fmt.Fprintf(b, "%s%s:\n", pad, key)
		for _, item := range v {
			fmt.Fprintf(b, "%s  - %s\n", pad, yamlScalar(item))
		}
	case []any:
		if len(v) == 0 {
			fmt.Fprintf(b, "%s%s: []\n", pad, key)
			return
		}
		fmt.Fprintf(b, "%s%s:\n", pad, key)
		for _, item := range v {
			if m, ok := item.(map[string]any); ok {
				keys := sortedKeys(m)
				for i, k := range keys {
					prefix := pad + "  - "
					if i > 0 {
						prefix = pad + "    "
					}
					fmt.Fprintf(b, "%s%s: %s\n", prefix, k, yamlValue(m[k]))
				}
				continue
			}
			fmt.Fprintf(b, "%s  - %s\n", pad, yamlValue(item))
		}
	case map[string]any:
		if len(v) == 0 {
			fmt.Fprintf(b, "%s%s: {}\n", pad, key)
			return
		}
		fmt.Fprintf(b, "%s%s:\n", pad, key)
		for _, k := range sortedKeys(v) {
			writeYAMLEntry(b, k, v[k], indent+1)
		}
	default:
		fmt.Fprintf(b, "%s%s: %s\n", pad, key, yamlValue(value))
	}
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func yamlValue(v any) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case bool:
		return strconv.FormatBool(t)
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case float64:
		return strconv.FormatFloat(t, 'g', -1, 64)
	case string:
		return yamlScalar(t)
	case []string:
		items := make([]string, 0, len(t))
		for _, s := range t {
			items = append(items, yamlScalar(s))
		}
		return "[" + strings.Join(items, ", ") + "]"
	case []any:
		items := make([]string, 0, len(t))
		for _, s := range t {
			items = append(items, yamlValue(s))
		}
		return "[" + strings.Join(items, ", ") + "]"
	case map[string]any:
		parts := make([]string, 0, len(t))
		for _, k := range sortedKeys(t) {
			parts = append(parts, k+": "+yamlValue(t[k]))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	default:
		return yamlScalar(fmt.Sprint(v))
	}
}

// yamlScalar quotes a string when a plain scalar would be ambiguous.
func yamlScalar(s string) string {
	if s == "" {
		return `""`
	}
	if needsQuoting(s) {
		return strconv.Quote(s)
	}
	return s
}

func needsQuoting(s string) bool {
	switch strings.ToLower(s) {
	case "true", "false", "null", "yes", "no", "on", "off", "~":
		return true
	}
	if s != strings.TrimSpace(s) {
		return true
	}
	if strings.ContainsAny(s, ":#\n\r\t\"'") {
		return true
	}
	switch s[0] {
	case '-', '?', '*', '&', '!', '|', '>', '%', '@', '`', '[', ']', '{', '}', ',':
		return true
	}
	if _, err := strconv.ParseFloat(s, 64); err == nil {
		return true
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

// InternalExtensionPrefix marks provider-extension keys that belong to Stemma
// itself (round-trip hints such as the original file name). They are never
// written into generated provider files.
const InternalExtensionPrefix = "stemma."

// ExtensionEntries returns the provider extensions of an entity as ordered
// front matter entries, skipping keys the exporter renders itself and every
// key in Stemma's reserved namespace.
func ExtensionEntries(ext canonical.Extensions, provider string, skip ...string) []KV {
	if ext == nil {
		return nil
	}
	m, ok := ext[provider]
	if !ok || len(m) == 0 {
		return nil
	}
	skipSet := make(map[string]struct{}, len(skip))
	for _, s := range skip {
		skipSet[s] = struct{}{}
	}
	out := make([]KV, 0, len(m))
	for _, k := range sortedKeys(m) {
		if _, drop := skipSet[k]; drop {
			continue
		}
		if strings.HasPrefix(k, InternalExtensionPrefix) {
			continue
		}
		out = append(out, KV{Key: k, Value: m[k]})
	}
	return out
}

// Markdown accumulates a generated Markdown document.
type Markdown struct {
	b strings.Builder
}

// Heading writes an ATX heading.
func (m *Markdown) Heading(level int, text string) {
	if level < 1 {
		level = 1
	}
	if level > 6 {
		level = 6
	}
	m.ensureBlankLine()
	fmt.Fprintf(&m.b, "%s %s\n", strings.Repeat("#", level), strings.TrimSpace(text))
}

// Paragraph writes a block of text followed by a blank line.
func (m *Markdown) Paragraph(text string) {
	text = strings.TrimRight(text, "\n")
	if strings.TrimSpace(text) == "" {
		return
	}
	m.ensureBlankLine()
	m.b.WriteString(text)
	m.b.WriteString("\n")
}

// Bullet writes a single list item, indenting continuation lines. A blank line
// is inserted before the first item of a list so the list is not glued to the
// preceding heading or paragraph.
func (m *Markdown) Bullet(text string) {
	if !m.endsWithListItem() {
		m.ensureBlankLine()
	}
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	for i, l := range lines {
		if i == 0 {
			fmt.Fprintf(&m.b, "- %s\n", strings.TrimSpace(l))
			continue
		}
		fmt.Fprintf(&m.b, "  %s\n", strings.TrimRight(l, " "))
	}
}

// endsWithListItem reports whether the buffer already ends inside a list.
func (m *Markdown) endsWithListItem() bool {
	s := strings.TrimRight(m.b.String(), "\n")
	if s == "" {
		return false
	}
	lines := strings.Split(s, "\n")
	last := lines[len(lines)-1]
	return strings.HasPrefix(last, "- ") || strings.HasPrefix(last, "  ")
}

// Raw appends text verbatim.
func (m *Markdown) Raw(text string) { m.b.WriteString(text) }

// BlankLine appends a blank line unless the buffer already ends with one.
func (m *Markdown) BlankLine() { m.ensureBlankLine() }

func (m *Markdown) ensureBlankLine() {
	s := m.b.String()
	if s == "" {
		return
	}
	if strings.HasSuffix(s, "\n\n") {
		return
	}
	if strings.HasSuffix(s, "\n") {
		m.b.WriteString("\n")
		return
	}
	m.b.WriteString("\n\n")
}

// Empty reports whether nothing has been written.
func (m *Markdown) Empty() bool { return strings.TrimSpace(m.b.String()) == "" }

// String returns the document with exactly one trailing newline.
func (m *Markdown) String() string {
	s := strings.TrimRight(m.b.String(), "\n")
	if s == "" {
		return ""
	}
	return s + "\n"
}

// FileSlug derives a safe file name component from a canonical name.
func FileSlug(name, fallback string) string {
	return canonical.SlugOrHash(name, fallback)
}
