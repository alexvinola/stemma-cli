package adapters

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/alexvinola/stemma-cli/internal/canonical"
	"github.com/alexvinola/stemma-cli/internal/capabilities"
	"github.com/alexvinola/stemma-cli/internal/diagnostics"
	"github.com/alexvinola/stemma-cli/internal/discovery"
	"github.com/alexvinola/stemma-cli/internal/workspace"
)

// SkillFile is the file every skill directory holds.
const SkillFile = "SKILL.md"

// NameRequest describes the one file, or the one skill directory, that an
// entity owns in a target namespace. Every exporter names such destinations
// through Builder.Destination, so one policy decides them for all targets:
//
//  1. An explicit profile directory or filename keeps precedence (through
//     Builder.Path), as before.
//  2. Hint, the name this target's own importer recorded, is reused when it
//     is a safe relative path. This is what keeps same-provider round trips
//     byte-identical; conflicting hints still block export.
//  3. Otherwise a name another provider's importer recorded (its "source
//     name", listed in capabilities Naming.SourceNames) is reused when it is
//     unambiguous, satisfies this target's name rule, keeps the destination
//     in the same discovered role, and collides with no other destination of
//     the export. See resolveNames for the collision rule.
//  4. Otherwise the destination uses the complete canonical ID (FileSlug or
//     SkillSlug), which distinct entities never share.
type NameRequest struct {
	// ID is the canonical entity that owns the destination.
	ID string
	// Dir is the target's default directory for the namespace.
	Dir string
	// Suffix completes a file name stem (".md", ".instructions.md"). A skill
	// request ignores it.
	Suffix string
	// Skill names a skill directory: the destination is Dir/<name>/SKILL.md.
	Skill bool
	// Hint is this target's own recorded name: a file name including Suffix,
	// or a skill directory name.
	Hint string
	// NestedHint allows Hint to name a file in a subdirectory of Dir.
	NestedHint bool
	// Ext holds the entity's extensions, where other providers' source names
	// are recorded.
	Ext canonical.Extensions
}

// nameNote explains, in the entity's mapping, a source name that was reused
// or could not be.
type nameNote struct {
	origin string // the provider(s) whose importer recorded the name
	field  string // where the name is stored, for the diagnostic detail
	name   string // the reused or rejected name
	used   bool
	reason string // why a recorded name was rejected
	path   string // the destination actually used
}

// namingState carries the shared naming decisions of one RunExport call.
// The first (probe) run records every candidate and every emitted path; the
// second run applies the resolved demotions.
type namingState struct {
	probe      bool
	candidates map[string]nameCandidate
	repeated   map[string]bool
	emissions  []emission
	// demoted maps an entity ID to the destinations its candidate collided
	// with, each as "<entities> at <path>".
	demoted map[string][]string
}

type nameCandidate struct {
	candidate, fallback string
	skill               bool
}

type emission struct {
	path     string
	entities []string
}

// RunExport runs an exporter under the shared naming policy. Collisions
// between reused source names can only be judged once every destination of
// the export is known, so the exporter runs twice: a probe run that reuses
// every eligible source name and records all emitted paths, then the real
// run, in which each colliding source name falls back to the canonical-ID
// name. Exporters are pure, so both runs see the same input; the probe run
// discards its output, diagnostics and token counts.
func RunExport(ctx context.Context, e Exporter, in ExportInput) (ExportResult, error) {
	probe := in
	probe.Tokens = nil
	probe.naming = &namingState{
		probe: true, candidates: map[string]nameCandidate{}, repeated: map[string]bool{},
	}
	if _, err := e.Export(ctx, probe); err != nil {
		return ExportResult{}, err
	}
	in.naming = &namingState{demoted: resolveNames(probe.naming)}
	return e.Export(ctx, in)
}

// resolveNames decides which source-derived names collide.
//
// Each accepted candidate claims a unit: its file, or for a skill its whole
// directory. A candidate collides when another destination of the export
// (any file, including hints, profile pins, aggregates, canonical-ID names
// and other candidates) has the same portable key as the unit, lies inside
// it, or is a file standing where one of the unit's parent directories must
// be. Every colliding candidate falls back, not one of them: no candidate is
// preferred over another, so the result depends on the set of destinations
// only, never on entity order. A fallback name can itself collide with a
// candidate that was clear before, so demotion repeats until nothing
// changes; it only ever adds demotions, so it terminates. Collisions left
// among hints, pins and canonical-ID names are blocked by Builder.Result.
func resolveNames(n *namingState) map[string][]string {
	fixed := []emission{}
	claimed := map[string]bool{}
	for _, e := range n.emissions {
		if len(e.entities) == 1 {
			id := e.entities[0]
			if c, ok := n.candidates[id]; ok && !n.repeated[id] && !claimed[id] && c.candidate == e.path {
				claimed[id] = true
				continue
			}
		}
		fixed = append(fixed, e)
	}
	ids := make([]string, 0, len(claimed))
	for id := range claimed {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	demoted := map[string][]string{}
	for id := range n.repeated {
		demoted[id] = []string{}
	}
	for {
		// owner is the entity whose candidate this is; label names every
		// entity written to the path, for the explanation.
		type dest struct{ path, owner, label string }
		dests := make([]dest, 0, len(fixed)+len(ids))
		for _, e := range fixed {
			dests = append(dests, dest{path: e.path, label: strings.Join(e.entities, ", ")})
		}
		for _, id := range ids {
			c := n.candidates[id]
			p := c.candidate
			if _, out := demoted[id]; out {
				p = c.fallback
			}
			dests = append(dests, dest{path: p, owner: id, label: id})
		}
		// Index every destination by its portable key and by the keys of
		// all its parent directories.
		byKey := map[string][]dest{}
		byAncestor := map[string][]dest{}
		for _, d := range dests {
			byKey[workspace.PortablePathKey(d.path)] = append(byKey[workspace.PortablePathKey(d.path)], d)
			for dir := path.Dir(d.path); dir != "."; dir = path.Dir(dir) {
				k := workspace.PortablePathKey(dir)
				byAncestor[k] = append(byAncestor[k], d)
			}
		}
		next := map[string][]string{}
		for _, id := range ids {
			if _, out := demoted[id]; out {
				continue
			}
			c := n.candidates[id]
			unit := c.candidate
			if c.skill {
				unit = path.Dir(c.candidate)
			}
			var hits []string
			other := func(ds []dest) {
				for _, d := range ds {
					if d.owner != id {
						hits = append(hits, d.label+" at "+d.path)
					}
				}
			}
			unitKey := workspace.PortablePathKey(unit)
			other(byKey[unitKey])
			other(byAncestor[unitKey])
			for dir := path.Dir(unit); dir != "."; dir = path.Dir(dir) {
				other(byKey[workspace.PortablePathKey(dir)])
			}
			if len(hits) > 0 {
				sort.Strings(hits)
				next[id] = dedupeStrings(hits)
			}
		}
		if len(next) == 0 {
			return demoted
		}
		for id, hits := range next {
			demoted[id] = hits
		}
	}
}

// Destination returns the destination path an entity owns under the shared
// naming policy described on NameRequest.
func (b *Builder) Destination(res Resolution, req NameRequest) string {
	layout := func(name string) (string, string) {
		if req.Skill {
			return path.Join(req.Dir, name), SkillFile
		}
		return req.Dir, name
	}
	if hint, ok := ValidHint(req.Hint, req.NestedHint); ok {
		dir, file := layout(hint)
		return b.Path(res, dir, file)
	}
	fallbackName := FileSlug(req.ID) + req.Suffix
	if req.Skill {
		fallbackName = SkillSlug(req.ID)
	}
	dir, file := layout(fallbackName)
	fallback := b.Path(res, dir, file)
	if fallback == "" {
		return ""
	}
	note, has := b.sourceName(req)
	if !has {
		return fallback
	}
	reject := func(reason string) string {
		note.used, note.reason, note.path = false, reason, fallback
		b.names[req.ID] = note
		return fallback
	}
	if note.reason != "" {
		return reject(note.reason)
	}
	name := note.name
	if !req.Skill {
		name += req.Suffix
	}
	dir, file = layout(name)
	candidate, err := joinDestination(res, dir, file)
	if err != nil {
		return reject("it cannot form a safe destination path")
	}
	if candidate == fallback {
		// A profile pin already decides the name; there is nothing to report.
		return fallback
	}
	if !sameRole(candidate, fallback) {
		return reject(fmt.Sprintf("%s would be discovered as a different kind of configuration file", candidate))
	}
	if n := b.in.naming; n != nil {
		if n.probe {
			if _, seen := n.candidates[req.ID]; seen {
				n.repeated[req.ID] = true
			}
			n.candidates[req.ID] = nameCandidate{candidate: candidate, fallback: fallback, skill: req.Skill}
		} else if hits, out := n.demoted[req.ID]; out {
			if len(hits) == 0 {
				return reject("it collides with another generated destination")
			}
			return reject("it collides with " + strings.Join(hits, ", "))
		}
	}
	note.used, note.path = true, candidate
	b.names[req.ID] = note
	return candidate
}

// sourceName returns the name other providers' importers recorded for the
// entity. has is false when none was recorded. A recorded name that cannot
// be used comes back with a reason.
func (b *Builder) sourceName(req NameRequest) (note nameNote, has bool) {
	rule := b.in.Capabilities.Naming.FileNames
	if req.Skill {
		rule = b.in.Capabilities.Naming.SkillDirectories
	}
	type found struct{ provider, field, raw, name string }
	var all []found
	for _, c := range capabilities.All() {
		if c.Target == b.target {
			continue
		}
		for _, h := range c.Naming.SourceNames {
			v, ok := req.Ext.Get(string(c.Target), h.Key)
			if !ok {
				continue
			}
			raw, _ := v.(string)
			all = append(all, found{
				provider: string(c.Target), field: "extensions." + string(c.Target) + "." + h.Key,
				raw: raw, name: sourceStem(raw, h),
			})
		}
	}
	if len(all) == 0 {
		return nameNote{}, false
	}
	names := map[string]bool{}
	for _, f := range all {
		if f.name == "" {
			return nameNote{origin: f.provider, field: f.field, name: displayName(f.raw),
				reason: "it is not a safe file or directory name with the expected suffix"}, true
		}
		names[f.name] = true
	}
	var providers, fields []string
	for _, f := range all {
		providers = append(providers, f.provider)
		fields = append(fields, fmt.Sprintf("%s=%q", f.field, displayName(f.raw)))
	}
	note = nameNote{origin: strings.Join(dedupeStrings(providers), ", "), field: strings.Join(fields, ", "),
		name: all[0].name}
	if len(names) > 1 {
		note.reason = "different source names were recorded for the entity"
		return note, true
	}
	if !rule.Allows(note.name) {
		note.reason = fmt.Sprintf("it does not satisfy this target's %s rule (at most %d bytes)",
			rule.Charset, rule.MaxLength)
	}
	return note, true
}

// maxDisplayedName bounds how much of an untrusted recorded name a diagnostic
// or explanation repeats.
const maxDisplayedName = 128

func displayName(raw string) string {
	if len(raw) <= maxDisplayedName {
		return raw
	}
	return strings.ToValidUTF8(raw[:maxDisplayedName], "") + "..."
}

// sourceStem strips a provider suffix from a recorded source name. The
// recorded value must be a normalized, safe relative path, a single segment
// unless the hint allows subdirectories; only its last segment is reused.
func sourceStem(raw string, h capabilities.SourceNameHint) string {
	clean, err := workspace.NormalizeRel(raw)
	if err != nil || clean != raw || !h.Nested && strings.Contains(clean, "/") {
		return ""
	}
	base := path.Base(clean)
	if h.Suffix != "" {
		if !strings.HasSuffix(base, h.Suffix) {
			return ""
		}
		base = strings.TrimSuffix(base, h.Suffix)
	}
	return base
}

// ValidHint reports whether a name recorded by the target's own importer can
// be reused: a normalized, safe relative path (workspace.NormalizeRel) that
// stays a single segment unless nested names are allowed.
func ValidHint(hint string, nested bool) (string, bool) {
	if hint == "" {
		return "", false
	}
	clean, err := workspace.NormalizeRel(hint)
	if err != nil || clean != hint {
		return "", false
	}
	if !nested && strings.Contains(clean, "/") {
		return "", false
	}
	return clean, true
}

// joinDestination mirrors Builder.Path without reporting a diagnostic.
func joinDestination(res Resolution, dir, file string) (string, error) {
	if res.Directory != "" {
		dir = res.Directory
	}
	if res.Filename != "" {
		file = res.Filename
	}
	return workspace.JoinRel(dir, file)
}

// sameRole reports whether discovery would classify both destinations the
// same way, so that a reused name never turns, say, a steering document into
// a file another provider loads by name.
func sameRole(a, b string) bool {
	fa, ra, oka := discovery.Classify(a)
	fb, rb, okb := discovery.Classify(b)
	return fa == fb && ra == rb && oka == okb
}

// nameExplanation extends a mapping explanation with its naming note.
func (b *Builder) nameExplanation(id string) (string, []string) {
	note, ok := b.names[id]
	if !ok {
		return "", nil
	}
	if note.used {
		return fmt.Sprintf(" The destination keeps the name %q of its %s source.", note.name, note.origin), nil
	}
	fp := b.Diag(diagnostics.New(diagnostics.SourceNameNotKept, diagnostics.SeverityInfo,
		"a source file or directory name was not reused for this target").
		WithEntity(id).WithPath(note.path).
		WithDetail("The %s source name %q (%s) was not used because %s. The destination uses the "+
			"complete canonical ID instead.", note.origin, note.name, note.field, note.reason).
		WithSuggestion("Rename the source, or pin a directory or filename for this entity in the target profile."))
	return fmt.Sprintf(" The %s source name %q was not reused because %s, so the destination uses the "+
		"complete canonical ID.", note.origin, note.name, note.reason), []string{fp}
}
