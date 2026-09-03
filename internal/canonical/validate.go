package canonical

import (
	"fmt"
	"strings"

	"github.com/alexvinola/stemma/internal/diagnostics"
	"github.com/alexvinola/stemma/internal/globs"
	"github.com/alexvinola/stemma/internal/provenance"
)

// Validate checks canonical invariants and returns diagnostics. It never
// mutates the project and never returns an error for user-caused problems:
// every user-visible failure is a diagnostic.
func Validate(p Project) []diagnostics.Diagnostic {
	var bag diagnostics.Bag

	if p.SchemaVersion != schemaVersion {
		bag.Add(diagnostics.New(diagnostics.UnknownSchema, diagnostics.SeverityError,
			fmt.Sprintf("canonical schema version %d is not supported", p.SchemaVersion)).
			WithDetail("This build of Stemma supports canonical schema version %d.", schemaVersion))
	}
	if strings.TrimSpace(p.ID) == "" {
		bag.Add(missing("project", "id"))
	}
	if strings.TrimSpace(p.Name) == "" {
		bag.Add(missing("project", "name"))
	}
	for _, t := range p.Targets {
		if !KnownTarget(t) {
			bag.Add(diagnostics.New(diagnostics.MissingRequired, diagnostics.SeverityError,
				fmt.Sprintf("unknown target %q in project targets", t)).
				WithSuggestion("Valid targets: %s.", targetList()))
		}
	}

	seen := map[string]string{}
	checkID := func(id string, t EntityType) {
		if strings.TrimSpace(id) == "" {
			bag.Add(diagnostics.New(diagnostics.InvalidEntityID, diagnostics.SeverityError,
				fmt.Sprintf("%s entity has an empty id", t)))
			return
		}
		gotType, slug, err := ParseID(id)
		switch {
		case err != nil:
			bag.Add(diagnostics.New(diagnostics.InvalidEntityID, diagnostics.SeverityError,
				err.Error()).WithEntity(id))
		case gotType != t:
			bag.Add(diagnostics.New(diagnostics.InvalidEntityID, diagnostics.SeverityError,
				fmt.Sprintf("entity id %q declares type %q but is stored as %q", id, gotType, t)).
				WithEntity(id))
		case !ValidIDSlug(slug):
			bag.Add(diagnostics.New(diagnostics.InvalidEntityID, diagnostics.SeverityError,
				fmt.Sprintf("entity id %q has a malformed slug", id)).
				WithEntity(id).
				WithSuggestion("Slugs use lowercase ASCII letters, digits and single hyphens."))
		}
		if prev, dup := seen[id]; dup {
			bag.Add(diagnostics.New(diagnostics.DuplicateEntityID, diagnostics.SeverityError,
				fmt.Sprintf("duplicate entity id %q", id)).
				WithEntity(id).
				WithDetail("The id is used by both a %s and a %s entity.", prev, t))
		}
		seen[id] = string(t)
	}

	for _, d := range p.ContextDocuments {
		checkID(d.ID, EntityContext)
		if strings.TrimSpace(d.Title) == "" {
			bag.Add(missing("context document", "title").WithEntity(d.ID))
		}
		if strings.TrimSpace(d.Content) == "" {
			bag.Add(missing("context document", "content").WithEntity(d.ID))
		}
		if !KnownContextKind(d.Kind) {
			bag.Add(diagnostics.New(diagnostics.MissingRequired, diagnostics.SeverityError,
				fmt.Sprintf("context document %q has unknown kind %q", d.ID, d.Kind)).WithEntity(d.ID))
		}
		if !KnownAudience(d.Audience) {
			bag.Add(diagnostics.New(diagnostics.MissingRequired, diagnostics.SeverityError,
				fmt.Sprintf("context document %q has unknown audience %q", d.ID, d.Audience)).WithEntity(d.ID))
		}
		bag.Extend(validateActivation(d.ID, d.Activation))
		bag.Extend(validateProvenance(d.ID, d.Provenance))
	}
	for _, r := range p.Rules {
		checkID(r.ID, EntityRule)
		if strings.TrimSpace(r.Title) == "" {
			bag.Add(missing("rule", "title").WithEntity(r.ID))
		}
		if strings.TrimSpace(r.Instruction) == "" {
			bag.Add(missing("rule", "instruction").WithEntity(r.ID))
		}
		if !KnownPriority(r.Priority) {
			bag.Add(diagnostics.New(diagnostics.MissingRequired, diagnostics.SeverityError,
				fmt.Sprintf("rule %q has unknown priority %q", r.ID, r.Priority)).
				WithEntity(r.ID).
				WithSuggestion("Priority must be one of: must, should, may."))
		}
		bag.Extend(validateActivation(r.ID, r.Activation))
		bag.Extend(validateProvenance(r.ID, r.Provenance))
	}
	for _, pr := range p.Procedures {
		checkID(pr.ID, EntityProcedure)
		if strings.TrimSpace(pr.Name) == "" {
			bag.Add(missing("procedure", "name").WithEntity(pr.ID))
		}
		if strings.TrimSpace(pr.Content) == "" {
			bag.Add(missing("procedure", "content").WithEntity(pr.ID))
		}
		bag.Extend(validateProvenance(pr.ID, pr.Provenance))
	}
	for _, s := range p.Skills {
		checkID(s.ID, EntitySkill)
		if strings.TrimSpace(s.Name) == "" {
			bag.Add(missing("skill", "name").WithEntity(s.ID))
		}
		if strings.TrimSpace(s.Content) == "" {
			bag.Add(missing("skill", "content").WithEntity(s.ID))
		}
		bag.Extend(validateProvenance(s.ID, s.Provenance))
	}
	for _, a := range p.Agents {
		checkID(a.ID, EntityAgent)
		if strings.TrimSpace(a.Name) == "" {
			bag.Add(missing("agent", "name").WithEntity(a.ID))
		}
		if strings.TrimSpace(a.Instructions) == "" {
			bag.Add(missing("agent", "instructions").WithEntity(a.ID))
		}
		for _, tool := range a.Tools {
			if unsafeToolName(tool) {
				bag.Add(diagnostics.New(diagnostics.AgentToolsNeedReview, diagnostics.SeverityError,
					fmt.Sprintf("agent %q declares an unsafe tool name %q", a.ID, sanitizeForMessage(tool))).
					WithEntity(a.ID).
					WithDetail("Tool names must not contain path separators, '..', control characters or NUL bytes.").
					WithSuggestion("Rename the tool in the canonical project, or remove it."))
			}
		}
		bag.Extend(validateProvenance(a.ID, a.Provenance))
	}
	for _, d := range p.Decisions {
		checkID(d.ID, EntityDecision)
		if strings.TrimSpace(d.Title) == "" {
			bag.Add(missing("decision", "title").WithEntity(d.ID))
		}
		if !KnownDecisionStatus(d.Status) {
			bag.Add(diagnostics.New(diagnostics.MissingRequired, diagnostics.SeverityError,
				fmt.Sprintf("decision %q has unknown status %q", d.ID, d.Status)).WithEntity(d.ID))
		}
		bag.Extend(validateProvenance(d.ID, d.Provenance))
	}

	blockIDs := map[string]struct{}{}
	for _, b := range p.OpaqueBlocks {
		if strings.TrimSpace(b.ID) == "" {
			bag.Add(missing("opaque block", "id"))
			continue
		}
		if _, dup := blockIDs[b.ID]; dup {
			bag.Add(diagnostics.New(diagnostics.DuplicateEntityID, diagnostics.SeverityError,
				fmt.Sprintf("duplicate opaque block id %q", b.ID)).WithEntity(b.ID))
		}
		blockIDs[b.ID] = struct{}{}
		if b.Hash != provenance.HashString(b.Content) {
			bag.Add(diagnostics.New(diagnostics.ManifestInvalid, diagnostics.SeverityError,
				fmt.Sprintf("opaque block %q hash does not match its content", b.ID)).
				WithEntity(b.ID).WithPath(b.SourcePath))
		}
		if strings.TrimSpace(b.Reason) == "" {
			bag.Add(missing("opaque block", "reason").WithEntity(b.ID))
		}
	}

	return bag.Items()
}

func targetList() string {
	parts := make([]string, 0, len(AllTargets()))
	for _, t := range AllTargets() {
		parts = append(parts, string(t))
	}
	return strings.Join(parts, ", ")
}

func missing(entity, field string) diagnostics.Diagnostic {
	return diagnostics.New(diagnostics.MissingRequired, diagnostics.SeverityError,
		fmt.Sprintf("%s is missing required field %q", entity, field))
}

func validateActivation(id string, a Activation) []diagnostics.Diagnostic {
	var out []diagnostics.Diagnostic
	if err := a.Validate(); err != nil {
		code := diagnostics.InvalidActivation
		if strings.Contains(err.Error(), "invalid glob pattern") {
			code = diagnostics.InvalidGlob
		}
		out = append(out, diagnostics.New(code, diagnostics.SeverityError,
			fmt.Sprintf("entity %q has an invalid activation: %v", id, err)).WithEntity(id))
		return out
	}
	for _, p := range append(append([]string{}, a.Include...), a.Exclude...) {
		if err := globs.Validate(p); err != nil {
			out = append(out, diagnostics.New(diagnostics.InvalidGlob, diagnostics.SeverityError,
				fmt.Sprintf("entity %q has an invalid pattern %q: %v", id, sanitizeForMessage(p), err)).
				WithEntity(id))
		}
	}
	return out
}

func validateProvenance(id string, pv provenance.Provenance) []diagnostics.Diagnostic {
	var out []diagnostics.Diagnostic
	if pv.SourcePath == "" && pv.SourceFormat == "" {
		// Hand-authored entities are allowed to carry no provenance.
		return nil
	}
	if pv.SourcePath == "" {
		out = append(out, diagnostics.New(diagnostics.DanglingProvenance, diagnostics.SeverityWarning,
			fmt.Sprintf("entity %q records a source format but no source path", id)).WithEntity(id))
	}
	if pv.SourceHash == "" {
		out = append(out, diagnostics.New(diagnostics.DanglingProvenance, diagnostics.SeverityWarning,
			fmt.Sprintf("entity %q records a source path but no source hash", id)).
			WithEntity(id).WithPath(pv.SourcePath))
	}
	if !pv.Disposition.Valid() {
		out = append(out, diagnostics.New(diagnostics.DanglingProvenance, diagnostics.SeverityError,
			fmt.Sprintf("entity %q has unknown provenance disposition %q", id, pv.Disposition)).
			WithEntity(id).WithPath(pv.SourcePath))
	}
	if pv.Span.ByteEnd != 0 && pv.Span.ByteEnd < pv.Span.ByteStart {
		out = append(out, diagnostics.New(diagnostics.DanglingProvenance, diagnostics.SeverityError,
			fmt.Sprintf("entity %q has an inverted provenance byte span", id)).
			WithEntity(id).WithPath(pv.SourcePath))
	}
	return out
}

func unsafeToolName(s string) bool {
	if s == "" || len(s) > 128 {
		return true
	}
	if strings.ContainsAny(s, "/\\\x00") || strings.Contains(s, "..") {
		return true
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

// sanitizeForMessage strips terminal control characters from untrusted text
// that is about to be embedded in a diagnostic message.
func sanitizeForMessage(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			fmt.Fprintf(&b, "\\x%02x", r)
			continue
		}
		b.WriteRune(r)
	}
	if b.Len() > 200 {
		return b.String()[:200] + "…"
	}
	return b.String()
}
