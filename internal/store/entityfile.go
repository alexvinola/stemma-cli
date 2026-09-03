package store

import (
	"fmt"
	"sort"
	"strings"

	"github.com/alexvinola/stemma-cli/internal/adapters"
	"github.com/alexvinola/stemma-cli/internal/canonical"
	"github.com/alexvinola/stemma-cli/internal/diagnostics"
	"github.com/alexvinola/stemma-cli/internal/parser"
)

// Body section headings recognised inside an entity file. They are how fields
// that are themselves prose are kept readable instead of being crammed into
// front matter.
const (
	sectionRationale    = "Rationale"
	sectionGoodExamples = "Good examples"
	sectionBadExamples  = "Bad examples"
	sectionContext      = "Context"
	sectionDecision     = "Decision"
	sectionConsequences = "Consequences"
	sectionConstraints  = "Agent constraints"
)

// renderActivation converts an activation into front matter entries. Only the
// fields that belong to the tag are written.
func renderActivation(a canonical.Activation) adapters.Ordered {
	out := adapters.Ordered{{Key: "type", Value: string(a.Type)}}
	switch a.Type {
	case canonical.ActivationPathScoped:
		out = append(out, adapters.KV{Key: "include", Value: anyList(a.Include)})
		if len(a.Exclude) > 0 {
			out = append(out, adapters.KV{Key: "exclude", Value: anyList(a.Exclude)})
		}
	case canonical.ActivationOnDemand:
		if a.Trigger != "" {
			out = append(out, adapters.KV{Key: "trigger", Value: a.Trigger})
		}
		if a.InvocationName != "" {
			out = append(out, adapters.KV{Key: "invocationName", Value: a.InvocationName})
		}
	}
	return out
}

// parseActivation reads an activation back, rejecting unknown tags.
func parseActivation(v any, path string) (canonical.Activation, error) {
	m, ok := v.(map[string]any)
	if !ok {
		return canonical.Activation{}, fmt.Errorf("%s: activation must be a mapping", path)
	}
	typeVal, _ := m["type"].(string)
	a := canonical.Activation{Type: canonical.ActivationType(typeVal)}
	if !canonical.KnownActivationType(a.Type) {
		return canonical.Activation{}, fmt.Errorf("%s: unknown activation type %q", path, typeVal)
	}
	a.Include = stringsOf(m["include"])
	a.Exclude = stringsOf(m["exclude"])
	a.Trigger, _ = m["trigger"].(string)
	a.InvocationName, _ = m["invocationName"].(string)
	return a, nil
}

func stringsOf(v any) []string {
	switch t := v.(type) {
	case nil:
		return nil
	case string:
		if t == "" {
			return nil
		}
		return []string{t}
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			if s, ok := item.(string); ok {
				out = append(out, s)
			} else {
				out = append(out, fmt.Sprint(item))
			}
		}
		return out
	default:
		return nil
	}
}

func anyList(in []string) []any {
	out := make([]any, 0, len(in))
	for _, s := range in {
		out = append(out, s)
	}
	return out
}

// extensionsValue renders provider extensions for front matter.
func extensionsValue(ext canonical.Extensions) (map[string]any, bool) {
	if len(ext) == 0 {
		return nil, false
	}
	out := map[string]any{}
	for provider, kv := range ext {
		if len(kv) == 0 {
			continue
		}
		inner := map[string]any{}
		for k, v := range kv {
			inner[k] = v
		}
		out[provider] = inner
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

func parseExtensions(v any) canonical.Extensions {
	m, ok := v.(map[string]any)
	if !ok || len(m) == 0 {
		return nil
	}
	out := canonical.Extensions{}
	for provider, raw := range m {
		inner, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		for k, val := range inner {
			out.Set(provider, k, val)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// body assembles an entity file body: the main prose, then optional sections.
type body struct {
	md adapters.Markdown
}

func (b *body) main(text string) {
	b.md.Paragraph(strings.TrimSpace(text))
}

func (b *body) section(heading, text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	b.md.Heading(2, heading)
	b.md.Paragraph(strings.TrimSpace(text))
}

func (b *body) bullets(heading string, items []string) {
	if len(items) == 0 {
		return
	}
	b.md.Heading(2, heading)
	for _, item := range items {
		b.md.Bullet(item)
	}
}

func (b *body) String() string { return b.md.String() }

// splitBody separates the leading prose from the recognised "## Heading"
// sections. Anything under an unrecognised heading stays part of the prose, so
// nothing a person writes is dropped.
func splitBody(doc parser.Document) (main string, sections map[string]string, lists map[string][]string) {
	sections = map[string]string{}
	lists = map[string][]string{}
	known := map[string]bool{
		sectionRationale: true, sectionGoodExamples: true, sectionBadExamples: true,
		sectionContext: true, sectionDecision: true, sectionConsequences: true,
		sectionConstraints: true,
	}
	var prose []string
	for _, s := range doc.Sections {
		if s.Level == 0 {
			prose = append(prose, strings.TrimSpace(s.Content))
			continue
		}
		if s.Level == 2 && known[s.Heading] {
			switch s.Heading {
			case sectionGoodExamples, sectionBadExamples, sectionConstraints:
				for _, b := range parser.Bullets(s.Content, 1) {
					lists[s.Heading] = append(lists[s.Heading], strings.TrimSpace(b.Text))
				}
			default:
				sections[s.Heading] = strings.TrimSpace(s.Content)
			}
			continue
		}
		// An unrecognised heading belongs to the prose, headings and all.
		text := strings.Repeat("#", s.Level) + " " + s.Heading
		if strings.TrimSpace(s.Content) != "" {
			text += "\n\n" + strings.TrimSpace(s.Content)
		}
		prose = append(prose, text)
	}
	return strings.TrimSpace(strings.Join(prose, "\n\n")), sections, lists
}

// kvSorted renders extension-style maps in a stable order.
func kvSorted(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// diagf builds a decoding diagnostic for an entity file.
func diagf(path, format string, args ...any) diagnostics.Diagnostic {
	return diagnostics.New(diagnostics.MissingRequired, diagnostics.SeverityError,
		fmt.Sprintf(format, args...)).WithPath(path)
}
