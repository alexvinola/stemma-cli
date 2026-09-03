package adapters

import (
	"fmt"
	"sort"
	"strings"

	"github.com/alexvinola/stemma-cli/internal/canonical"
	"github.com/alexvinola/stemma-cli/internal/profiles"
	"github.com/alexvinola/stemma-cli/internal/provenance"
)

// Resolution is the outcome of applying a target profile to one entity.
type Resolution struct {
	// Included reports whether the entity should be projected at all.
	Included bool
	// SkippedReason explains an exclusion (empty when Included).
	SkippedReason string
	// Activation is the activation to use for this target.
	Activation canonical.Activation
	// Directory and Filename pin the destination when the profile sets them.
	Directory string
	Filename  string
	// Content is the agent-facing text, after any content override.
	Content string
	// ContentOverridden reports that Content differs from the canonical text.
	ContentOverridden bool
	// AcceptLossy reports that the user accepted a lossy mapping here.
	AcceptLossy bool
	// Applied summarises the override for the projection report.
	Applied *AppliedOverride
}

// Resolve applies a profile override to an entity's activation and content.
//
// Activation defaults are honoured unless the profile replaces them; a
// documentation-only entity is never projected to agent-facing output unless
// the profile explicitly overrides its activation.
func Resolve(
	entityID string,
	enabled bool,
	activation canonical.Activation,
	content string,
	profile profiles.Profile,
) Resolution {
	res := Resolution{Included: true, Activation: activation, Content: content}

	if !enabled {
		res.Included = false
		res.SkippedReason = "the entity is disabled in the canonical project"
	}

	override, has := profile.For(entityID)
	if !has {
		if res.Included && activation.Type == canonical.ActivationDocumentationOnly {
			res.Included = false
			res.SkippedReason = "activation is documentation-only, so the content is never sent to an agent"
		}
		return res
	}

	applied := &AppliedOverride{
		Directory:   override.Directory,
		Filename:    override.Filename,
		AcceptLossy: override.AcceptLossy,
	}
	if override.Activation != nil {
		res.Activation = *override.Activation
		applied.Activation = override.Activation
	}
	if override.Directory != "" {
		res.Directory = override.Directory
	}
	if override.Filename != "" {
		res.Filename = override.Filename
	}
	if override.ContentOverride != "" {
		res.Content = override.ContentOverride
		res.ContentOverridden = true
		applied.ContentOverride = true
	}
	res.AcceptLossy = override.AcceptLossy

	if override.Include != nil {
		applied.Include = override.Include
		if !*override.Include {
			res.Included = false
			res.SkippedReason = "the target profile excludes this entity"
		} else if !enabled {
			// An explicit include cannot resurrect a disabled entity: the
			// canonical model stays the source of truth for enablement.
			res.Included = false
			res.SkippedReason = "the entity is disabled in the canonical project"
		} else {
			res.Included = true
			res.SkippedReason = ""
		}
	}
	if res.Included && res.Activation.Type == canonical.ActivationDocumentationOnly {
		res.Included = false
		res.SkippedReason = "activation is documentation-only, so the content is never sent to an agent"
	}
	res.Applied = applied
	return res
}

// SkipMapping builds the mapping for an entity that is deliberately not
// projected.
func SkipMapping(
	id string,
	kind canonical.EntityType,
	target canonical.TargetFormat,
	res Resolution,
	source provenance.Provenance,
) ProjectionMapping {
	return ProjectionMapping{
		EntityID:    id,
		EntityType:  kind,
		Target:      target,
		Outcome:     OutcomeSkipped,
		Files:       []string{},
		Diagnostics: []string{},
		Explanation: "Not projected: " + res.SkippedReason + ".",
		Activation:  res.Activation,
		Source:      source,
		Override:    res.Applied,
	}
}

// ReuseOriginal reports whether the original bytes of destPath can be
// re-emitted verbatim instead of regenerating the file.
//
// Reuse is only safe when every entity written to the file came from that same
// file, the file has not changed since import, the entity is still exactly what
// the importer produced, the file produced no other entities, and no profile
// override changes its delivery. This is what makes a same-format round trip
// with no semantic change byte-identical — and what stops an edit to
// .stemma/project.json from being silently discarded.
func ReuseOriginal(in ExportInput, destPath string, entityIDs []string) ([]byte, bool) {
	original, ok := in.Originals[destPath]
	if !ok {
		return nil, false
	}
	imported := append([]string{}, in.SourceIndex[destPath]...)
	got := append([]string{}, entityIDs...)
	sort.Strings(imported)
	sort.Strings(got)
	if len(imported) == 0 || len(imported) != len(got) {
		return nil, false
	}
	for i := range imported {
		if imported[i] != got[i] {
			return nil, false
		}
	}
	for _, id := range entityIDs {
		if o, has := in.Profile.For(id); has {
			if o.Include != nil || o.Activation != nil || o.Directory != "" ||
				o.Filename != "" || o.ContentOverride != "" {
				return nil, false
			}
		}
		pv, found := ProvenanceOf(in.Project, id)
		if !found || pv.SourcePath != destPath || pv.SourceHash != original.Hash {
			return nil, false
		}
		// The canonical entity must still match what was imported. An empty
		// recorded hash means "unknown", which is never treated as a match.
		current, ok := canonical.EntityFingerprint(in.Project, id)
		if !ok || pv.ContentHash == "" || pv.ContentHash != current {
			return nil, false
		}
	}
	return original.Data, true
}

// ProvenanceOf looks up an entity's provenance by ID.
func ProvenanceOf(p canonical.Project, id string) (provenance.Provenance, bool) {
	for _, e := range p.ContextDocuments {
		if e.ID == id {
			return e.Provenance, true
		}
	}
	for _, e := range p.Rules {
		if e.ID == id {
			return e.Provenance, true
		}
	}
	for _, e := range p.Procedures {
		if e.ID == id {
			return e.Provenance, true
		}
	}
	for _, e := range p.Skills {
		if e.ID == id {
			return e.Provenance, true
		}
	}
	for _, e := range p.Agents {
		if e.ID == id {
			return e.Provenance, true
		}
	}
	for _, e := range p.Decisions {
		if e.ID == id {
			return e.Provenance, true
		}
	}
	return provenance.Provenance{}, false
}

// ScopeLabel renders an activation as a short human-readable label.
func ScopeLabel(a canonical.Activation) string {
	switch a.Type {
	case canonical.ActivationAlways:
		return "always-on"
	case canonical.ActivationPathScoped:
		label := "paths " + strings.Join(a.Include, ", ")
		if len(a.Exclude) > 0 {
			label += " (excluding " + strings.Join(a.Exclude, ", ") + ")"
		}
		return label
	case canonical.ActivationOnDemand:
		if a.InvocationName != "" {
			return "on demand (" + a.InvocationName + ")"
		}
		return "on demand"
	case canonical.ActivationDocumentationOnly:
		return "documentation only"
	default:
		return fmt.Sprintf("unknown activation %q", a.Type)
	}
}
