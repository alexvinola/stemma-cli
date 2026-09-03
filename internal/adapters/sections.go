package adapters

import (
	"strings"

	"github.com/alexvinola/stemma/internal/canonical"
	"github.com/alexvinola/stemma/internal/parser"
	"github.com/alexvinola/stemma/internal/provenance"
)

// Unit is a heading-delimited part of an aggregate instructions file.
type Unit struct {
	// Title is the heading text, or "" for the leading preamble.
	Title string
	// Level is the heading level, or 0 for the preamble.
	Level int
	// Content is the body of the unit, including any nested subheadings, with
	// the unit's own heading line removed.
	Content string
	// Span locates the unit in the source file.
	Span provenance.Span
}

// SplitDocument divides an aggregate instructions file into units.
//
// The split level is chosen structurally: if the document opens with a single
// top-level heading that acts as a title, the split happens one level deeper,
// so that the title's own text becomes the preamble. Otherwise the document is
// split at its shallowest heading level. Nested subheadings stay inside their
// parent unit, so no heading text is ever lost.
func SplitDocument(doc parser.Document) []Unit {
	sections := doc.Sections
	if len(sections) == 0 {
		if strings.TrimSpace(doc.Body) == "" {
			return nil
		}
		return []Unit{{
			Content: strings.TrimSpace(doc.Body),
			Span:    provenance.Span{LineStart: doc.BodyLine, ByteStart: doc.BodyByte},
		}}
	}

	minLevel := 0
	titleCount := 0
	for _, s := range sections {
		if s.Level == 0 {
			continue
		}
		if minLevel == 0 || s.Level < minLevel {
			minLevel = s.Level
		}
	}
	if minLevel == 0 {
		// No headings at all: one preamble unit.
		return []Unit{{Content: strings.TrimSpace(sections[0].Content), Span: sections[0].Span}}
	}
	for _, s := range sections {
		if s.Level == minLevel {
			titleCount++
		}
	}
	splitLevel := minLevel
	firstIsTitle := sections[0].Level == minLevel && titleCount == 1
	if firstIsTitle {
		next := 0
		for _, s := range sections {
			if s.Level > minLevel && (next == 0 || s.Level < next) {
				next = s.Level
			}
		}
		if next != 0 {
			splitLevel = next
		}
	}

	var units []Unit
	var current *Unit
	flush := func() {
		if current == nil {
			return
		}
		current.Content = strings.TrimSpace(current.Content)
		units = append(units, *current)
		current = nil
	}
	appendContent := func(text string) {
		if current == nil {
			return
		}
		if current.Content != "" && text != "" {
			current.Content += "\n\n"
		}
		current.Content += text
	}
	for _, s := range sections {
		switch {
		case s.Level == 0 || s.Level < splitLevel:
			// Preamble or a heading above the split level: its own text starts
			// (or extends) the preamble unit.
			if current == nil || current.Level != 0 {
				flush()
				current = &Unit{Level: 0, Title: "", Span: s.Span}
			}
			if s.Level > 0 && s.Level < splitLevel && strings.TrimSpace(s.Content) == "" {
				// A pure title heading with no body contributes nothing.
				continue
			}
			appendContent(strings.TrimSpace(s.Content))
			current.Span.ByteEnd = s.Span.ByteEnd
			current.Span.LineEnd = s.Span.LineEnd
		case s.Level == splitLevel:
			flush()
			current = &Unit{Level: s.Level, Title: s.Heading, Content: strings.TrimSpace(s.Content), Span: s.Span}
		default:
			// Deeper heading: fold it back into the current unit, preserving
			// its heading text.
			text := strings.Repeat("#", s.Level) + " " + s.Heading
			if strings.TrimSpace(s.Content) != "" {
				text += "\n\n" + strings.TrimSpace(s.Content)
			}
			if current == nil {
				current = &Unit{Level: splitLevel, Title: "", Span: s.Span}
			}
			appendContent(text)
			current.Span.ByteEnd = s.Span.ByteEnd
			current.Span.LineEnd = s.Span.LineEnd
		}
	}
	flush()

	out := units[:0]
	for _, u := range units {
		if u.Title == "" && strings.TrimSpace(u.Content) == "" {
			continue
		}
		out = append(out, u)
	}
	return out
}

// knownHeadings maps documented section headings to canonical context kinds.
//
// Only exact, well-known headings are mapped. Anything else becomes
// KindOther: Stemma never infers a kind from arbitrary prose.
var knownHeadings = map[string]canonical.ContextKind{
	"api design":            canonical.KindConventions,
	"architecture":          canonical.KindArchitecture,
	"architecture overview": canonical.KindArchitecture,
	"build and test":        canonical.KindOperations,
	"code style":            canonical.KindConventions,
	"coding conventions":    canonical.KindConventions,
	"coding standards":      canonical.KindConventions,
	"coding style":          canonical.KindConventions,
	"commands":              canonical.KindOperations,
	"conventions":           canonical.KindConventions,
	"deployment":            canonical.KindOperations,
	"development workflow":  canonical.KindOperations,
	"domain":                canonical.KindDomain,
	"domain model":          canonical.KindDomain,
	"glossary":              canonical.KindDomain,
	"operations":            canonical.KindOperations,
	"overview":              canonical.KindProduct,
	"product":               canonical.KindProduct,
	"product overview":      canonical.KindProduct,
	"project overview":      canonical.KindProduct,
	"project structure":     canonical.KindStructure,
	"repository structure":  canonical.KindStructure,
	"security":              canonical.KindSecurity,
	"security requirements": canonical.KindSecurity,
	"stack":                 canonical.KindTechnology,
	"structure":             canonical.KindStructure,
	"tech stack":            canonical.KindTechnology,
	"technology":            canonical.KindTechnology,
	"technology stack":      canonical.KindTechnology,
	"testing":               canonical.KindTesting,
	"testing conventions":   canonical.KindTesting,
	"testing strategy":      canonical.KindTesting,
	"tests":                 canonical.KindTesting,
}

// KindFromHeading maps a known heading to a context kind, or KindOther.
func KindFromHeading(heading string) canonical.ContextKind {
	if k, ok := knownHeadings[strings.ToLower(strings.TrimSpace(heading))]; ok {
		return k
	}
	return canonical.KindOther
}

// KnownHeadings returns the recognised headings, for documentation.
func KnownHeadings() map[string]canonical.ContextKind {
	out := make(map[string]canonical.ContextKind, len(knownHeadings))
	for k, v := range knownHeadings {
		out[k] = v
	}
	return out
}
