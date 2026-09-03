package adapters

import (
	"fmt"
	"sort"
	"strings"

	"github.com/alexvinola/stemma-cli/internal/canonical"
	"github.com/alexvinola/stemma-cli/internal/diagnostics"
	"github.com/alexvinola/stemma-cli/internal/provenance"
	"github.com/alexvinola/stemma-cli/internal/tokenestimate"
	"github.com/alexvinola/stemma-cli/internal/workspace"
)

// Builder accumulates the output of one export run.
//
// It exists so that every adapter records files, mappings, diagnostics and
// token costs the same way, which is what makes "exactly one outcome per
// entity" checkable.
type Builder struct {
	target   canonical.TargetFormat
	in       ExportInput
	files    map[string]GeneratedFile
	mappings []ProjectionMapping
	bag      diagnostics.Bag
	opaque   map[string][]canonical.OpaqueBlock
	emitted  map[string]bool
}

// NewBuilder starts an export run.
func NewBuilder(target canonical.TargetFormat, in ExportInput) *Builder {
	b := &Builder{
		target:  target,
		in:      in,
		files:   map[string]GeneratedFile{},
		opaque:  map[string][]canonical.OpaqueBlock{},
		emitted: map[string]bool{},
	}
	for _, blk := range in.Project.OpaqueBlocks {
		b.opaque[blk.SourcePath] = append(b.opaque[blk.SourcePath], blk)
	}
	return b
}

// Resolve applies the target profile to an entity.
func (b *Builder) Resolve(id string, enabled bool, activation canonical.Activation, content string) Resolution {
	return Resolve(id, enabled, activation, content, b.in.Profile)
}

// ResolveContext resolves a context document, additionally honouring its
// audience: a document written for people is never sent to an agent.
func (b *Builder) ResolveContext(doc canonical.ContextDocument) Resolution {
	res := b.Resolve(doc.ID, canonical.IsEnabled(doc.Enabled), doc.Activation, doc.Content)
	if res.Included && doc.Audience == canonical.AudienceHuman {
		res.Included = false
		res.SkippedReason = "the document's audience is \"human\", so it is not projected into agent context"
	}
	return res
}

// Diag records a diagnostic and returns its fingerprint.
func (b *Builder) Diag(d diagnostics.Diagnostic) string {
	if d.Target == "" {
		d = d.WithTarget(string(b.target))
	}
	b.bag.Add(d)
	return d.Fingerprint
}

// Path resolves the destination path for an entity, honouring profile pins.
func (b *Builder) Path(res Resolution, defaultDir, defaultFile string) string {
	dir, file := defaultDir, defaultFile
	if res.Directory != "" {
		dir = res.Directory
	}
	if res.Filename != "" {
		file = res.Filename
	}
	p, err := workspace.JoinRel(dir, file)
	if err != nil {
		b.Diag(diagnostics.New(diagnostics.PathEscape, diagnostics.SeverityError,
			fmt.Sprintf("refusing to generate an unsafe path from directory %q and file %q", dir, file)).
			WithDetail("%v", err))
		return ""
	}
	return p
}

// Emit records a generated file.
//
// When the destination is also the file the content was imported from, and
// nothing that file produced has changed, the original bytes are re-emitted
// verbatim. That is what makes a same-format round trip with no semantic
// change byte-identical, including line endings and any byte order mark.
func (b *Builder) Emit(path, content string, entities []string) {
	if path == "" {
		return
	}
	ids := append([]string{}, entities...)
	sort.Strings(ids)
	if _, already := b.files[path]; !already {
		if original, ok := ReuseOriginal(b.in, path, ids); ok {
			b.EmitReused(path, original, ids)
			return
		}
		if _, wasSource := b.in.Originals[path]; wasSource {
			// Regeneration is never silent. This is informational rather than a
			// warning because regenerating after a real change is the normal
			// path, and `check --warnings-as-errors` should not fail on it.
			b.Diag(diagnostics.New(diagnostics.RegeneratedFile, diagnostics.SeverityInfo,
				"the file was regenerated rather than re-emitted unchanged").
				WithPath(path).
				WithDetail("Stemma could not prove that a minimal patch was safe, so it rewrote the " +
					"file. Preserved provider content is written back, but hand-made formatting in " +
					"the parts Stemma models is not.").
				WithSuggestion("Review the diff before applying."))
		}
	}
	if existing, ok := b.files[path]; ok {
		merged := append(existing.Entities, ids...)
		sort.Strings(merged)
		b.files[path] = GeneratedFile{
			Path: path, Content: []byte(existing.Text + content), Text: existing.Text + content,
			Mode: 0o644, Entities: dedupeStrings(merged),
		}
		return
	}
	b.files[path] = GeneratedFile{
		Path: path, Content: []byte(content), Text: content, Mode: 0o644, Entities: dedupeStrings(ids),
	}
}

// EmitReused records a file re-emitted verbatim from its original bytes.
func (b *Builder) EmitReused(path string, content []byte, entities []string) {
	if path == "" {
		return
	}
	ids := append([]string{}, entities...)
	sort.Strings(ids)
	b.files[path] = GeneratedFile{
		Path: path, Content: content, Text: string(content), Mode: 0o644,
		ReusedSource: true, Entities: dedupeStrings(ids),
	}
}

// Record adds a projection mapping.
func (b *Builder) Record(
	id string, kind canonical.EntityType, outcome Outcome,
	res Resolution, prov provenance.Provenance, files []string, explanation string,
) {
	b.RecordWithDiagnostics(id, kind, outcome, res, prov, files, explanation, nil)
}

// RecordWithDiagnostics adds a mapping that references specific diagnostics.
func (b *Builder) RecordWithDiagnostics(
	id string, kind canonical.EntityType, outcome Outcome,
	res Resolution, prov provenance.Provenance, files []string, explanation string, diagIDs []string,
) {
	if res.ContentOverridden {
		diagIDs = append(diagIDs, b.Diag(diagnostics.New(diagnostics.TargetOverridesContent,
			diagnostics.SeverityWarning,
			"the target profile replaces the canonical text for this entity").
			WithEntity(id).
			WithDetail("%s no longer uses the canonical wording for target %s.", id, b.target).
			WithSuggestion("Remove contentOverride from the profile to use the canonical text.")))
		if outcome == OutcomeExact {
			outcome = OutcomeAdapted
		}
	}
	if res.AcceptLossy && outcome == OutcomeLossy {
		explanation += " The lossy mapping is explicitly accepted in the target profile."
	}
	sorted := make([]string, 0, len(files))
	for _, f := range files {
		if f != "" {
			sorted = append(sorted, f)
		}
	}
	sort.Strings(sorted)
	// An entity that should have been written somewhere but has no safe
	// destination is blocked, never reported as a successful projection.
	if len(sorted) == 0 && outcome != OutcomeSkipped && outcome != OutcomeBlocked {
		outcome = OutcomeBlocked
		explanation = "No safe destination path could be produced for this entity, so it was not written. " +
			explanation
	}
	dg := append([]string{}, diagIDs...)
	sort.Strings(dg)
	if dg == nil {
		dg = []string{}
	}
	b.mappings = append(b.mappings, ProjectionMapping{
		EntityID:    id,
		EntityType:  kind,
		Target:      b.target,
		Outcome:     outcome,
		Files:       sorted,
		Diagnostics: dedupeStrings(dg),
		Explanation: strings.TrimSpace(explanation),
		Activation:  res.Activation,
		Source:      prov,
		Override:    res.Applied,
		Tokens:      tokenestimate.Default().Estimate(res.Content),
	})
}

// Exact records a faithful projection.
func (b *Builder) Exact(id string, kind canonical.EntityType, res Resolution,
	prov provenance.Provenance, files []string, explanation string) {
	b.Record(id, kind, OutcomeExact, res, prov, files, explanation)
}

// Adapted records a projection that changed the delivery mechanism.
func (b *Builder) Adapted(id string, kind canonical.EntityType, res Resolution,
	prov provenance.Provenance, files []string, explanation string) {
	b.Record(id, kind, OutcomeAdapted, res, prov, files, explanation)
}

// Skip records an entity that was deliberately not projected.
func (b *Builder) Skip(id string, kind canonical.EntityType, res Resolution, prov provenance.Provenance) {
	b.mappings = append(b.mappings, SkipMapping(id, kind, b.target, res, prov))
}

// Invariant records an unreachable activation case. Reaching it is a compiler
// bug, not user error, so it is reported as a blocking internal diagnostic
// rather than being silently ignored.
func (b *Builder) Invariant(id string, kind canonical.EntityType, res Resolution, prov provenance.Provenance) {
	fp := b.Diag(diagnostics.New(diagnostics.InternalInvariant, diagnostics.SeverityError,
		fmt.Sprintf("no projection rule for activation %q", res.Activation.Type)).
		WithEntity(id).
		WithDetail("This is a Stemma bug: every activation type must have an explicit projection.").
		WithSuggestion("Please report the entity id and target."))
	b.RecordWithDiagnostics(id, kind, OutcomeBlocked, res, prov, nil,
		"No projection rule matched this activation type.", []string{fp})
}

// CountAlwaysOn records always-on token cost.
func (b *Builder) CountAlwaysOn(s string) {
	if b.in.Tokens != nil {
		b.in.Tokens.AddAlwaysOn(s)
	}
}

// CountScoped records path-scoped token cost.
func (b *Builder) CountScoped(scope, entityID, s string) {
	if b.in.Tokens != nil {
		b.in.Tokens.AddScoped(scope, entityID, s)
	}
}

// CountOnDemand records on-demand token cost.
func (b *Builder) CountOnDemand(s string) {
	if b.in.Tokens != nil {
		b.in.Tokens.AddOnDemand(s)
	}
}

// CountDocumentationOnly records content that never reaches an agent.
func (b *Builder) CountDocumentationOnly(s string) {
	if b.in.Tokens != nil && strings.TrimSpace(s) != "" {
		b.in.Tokens.AddDocumentationOnly(s)
	}
}

// ReemitOpaque returns the preserved blocks that belong to the given source
// path and to this target, marking them as emitted.
func (b *Builder) ReemitOpaque(sourcePath string) string {
	blocks := b.opaque[sourcePath]
	if len(blocks) == 0 {
		return ""
	}
	sort.Slice(blocks, func(i, j int) bool { return blocks[i].Span.ByteStart < blocks[j].Span.ByteStart })
	var out strings.Builder
	for _, blk := range blocks {
		if blk.Provider != string(b.target) || !blk.ReemitForRoundTrip {
			continue
		}
		b.emitted[blk.ID] = true
		out.WriteString("\n")
		out.WriteString(strings.TrimRight(blk.Content, "\n"))
		out.WriteString("\n")
		b.mappings = append(b.mappings, ProjectionMapping{
			EntityID: blk.ID, EntityType: canonical.EntityOpaque, Target: b.target,
			Outcome: OutcomeExact, Files: []string{sourcePath}, Diagnostics: []string{},
			Explanation: "Preserved provider content was written back verbatim.",
			Activation:  canonical.Always(),
		})
	}
	return out.String()
}

// EmitOpaqueFile writes a preserved block back as its own file, which is how
// a provider file Stemma does not model survives a round trip unchanged.
func (b *Builder) EmitOpaqueFile(blk canonical.OpaqueBlock) {
	if blk.Provider != string(b.target) || !blk.ReemitForRoundTrip || blk.SourcePath == "" {
		return
	}
	b.emitted[blk.ID] = true
	b.Emit(blk.SourcePath, blk.Content, []string{blk.ID})
	b.mappings = append(b.mappings, ProjectionMapping{
		EntityID: blk.ID, EntityType: canonical.EntityOpaque, Target: b.target,
		Outcome: OutcomeExact, Files: []string{blk.SourcePath}, Diagnostics: []string{},
		Explanation: "Preserved provider content was written back to its own file verbatim.",
		Activation:  canonical.Always(),
	})
}

// OpaqueBlocksFor returns the preserved blocks belonging to this target,
// sorted by identifier.
func (b *Builder) OpaqueBlocksFor() []canonical.OpaqueBlock {
	var out []canonical.OpaqueBlock
	for _, blk := range b.in.Project.OpaqueBlocks {
		if blk.Provider == string(b.target) {
			out = append(out, blk)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// ReportUnemittedOpaque gives every preserved block that was not written back
// an explicit projection outcome.
func (b *Builder) ReportUnemittedOpaque() {
	blocks := append([]canonical.OpaqueBlock{}, b.in.Project.OpaqueBlocks...)
	sort.Slice(blocks, func(i, j int) bool { return blocks[i].ID < blocks[j].ID })
	for _, blk := range blocks {
		if b.emitted[blk.ID] {
			continue
		}
		if blk.Provider != string(b.target) {
			b.mappings = append(b.mappings, ProjectionMapping{
				EntityID: blk.ID, EntityType: canonical.EntityOpaque, Target: b.target,
				Outcome: OutcomeSkipped, Files: []string{}, Diagnostics: []string{},
				Explanation: fmt.Sprintf(
					"Preserved %s content has no representation in %s; it stays in the canonical project.",
					blk.Provider, b.target),
				Activation: canonical.DocumentationOnly(),
			})
			continue
		}
		fp := b.Diag(diagnostics.New(diagnostics.OpaqueNotReemitted, diagnostics.SeverityWarning,
			"preserved provider content was not written back into the generated output").
			WithEntity(blk.ID).WithPath(blk.SourcePath).
			WithDetail("Reason it was preserved: %s. Its source file is no longer generated by this target.",
				blk.Reason).
			WithSuggestion("Check %s manually; the content is still stored in .stemma/project.json.",
				blk.SourcePath))
		b.mappings = append(b.mappings, ProjectionMapping{
			EntityID: blk.ID, EntityType: canonical.EntityOpaque, Target: b.target,
			Outcome: OutcomeLossy, Files: []string{}, Diagnostics: []string{fp},
			Explanation: "Preserved content could not be written back to its source file.",
			Activation:  canonical.DocumentationOnly(),
		})
	}
}

// Result finalises the export deterministically.
func (b *Builder) Result() ExportResult {
	files := make([]GeneratedFile, 0, len(b.files))
	for _, f := range b.files {
		if f.Entities == nil {
			f.Entities = []string{}
		}
		files = append(files, f)
	}
	SortFiles(files)
	mappings := append([]ProjectionMapping{}, b.mappings...)
	SortMappings(mappings)
	for i := range mappings {
		if mappings[i].Files == nil {
			mappings[i].Files = []string{}
		}
		if mappings[i].Diagnostics == nil {
			mappings[i].Diagnostics = []string{}
		}
	}
	return ExportResult{Files: files, Mappings: mappings, Diagnostics: b.bag.Items()}
}

func dedupeStrings(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]struct{}{}
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

// RuleLine returns the agent-facing text of a rule.
func RuleLine(r canonical.Rule) string { return strings.TrimSpace(r.Instruction) }

// AgentOutcome decides how faithfully a specialist agent maps to a target and
// returns the diagnostics that justify the outcome.
func AgentOutcome(
	agent canonical.Agent, target canonical.TargetFormat, b *Builder, res Resolution,
) (Outcome, string, []string) {
	if !b.in.Capabilities.NativeAgents {
		fp := b.Diag(diagnostics.New(diagnostics.AgentNotNative, diagnostics.SeverityWarning,
			"there is no native specialist-agent format for this target").
			WithEntity(agent.ID).WithTarget(string(target)).
			WithDetail("The definition of %q is flattened into ordinary guidance.", agent.Name))
		return OutcomeLossy, fmt.Sprintf(
			"%s has no native specialist-agent format, so the definition is flattened into ordinary guidance.",
			target), []string{fp}
	}
	if agent.Provenance.SourceFormat == string(target) {
		return OutcomeExact, fmt.Sprintf("%s supports specialist agents natively.", target), nil
	}
	if len(agent.Tools) > 0 {
		fp := b.Diag(diagnostics.New(diagnostics.AgentToolsNeedReview, diagnostics.SeverityWarning,
			"agent tool names were carried over from another provider and need review").
			WithEntity(agent.ID).WithTarget(string(target)).
			WithDetail("The agent was imported from %q and declares tools: %s. Stemma never "+
				"translates tool names between providers.",
				agent.Provenance.SourceFormat, strings.Join(agent.Tools, ", ")).
			WithSuggestion("Check that each tool name exists in %s, or remove the list.", target))
		return OutcomeLossy, fmt.Sprintf(
			"%s supports specialist agents, but the declared tool names come from %s and are not "+
				"verified against this provider.", target, agent.Provenance.SourceFormat), []string{fp}
	}
	return OutcomeAdapted, fmt.Sprintf(
		"%s supports specialist agents; the definition was imported from %s and re-rendered in this "+
			"provider's format.", target, agent.Provenance.SourceFormat), nil
}

// AlwaysBucket accumulates the content of an always-on instructions file.
type AlwaysBucket struct {
	sections  []alwaysSection
	rules     []alwaysRule
	decisions []alwaysDecision
	ids       []string
}

type alwaysSection struct {
	id, title, content string
}

type alwaysRule struct {
	id       string
	priority canonical.Priority
	text     string
}

type alwaysDecision struct {
	id, title   string
	constraints []string
}

// NewAlwaysBucket returns an empty bucket.
func NewAlwaysBucket() *AlwaysBucket { return &AlwaysBucket{} }

// AddSection adds a context document section.
func (a *AlwaysBucket) AddSection(id, title, content string) {
	a.sections = append(a.sections, alwaysSection{id: id, title: title, content: content})
	a.ids = append(a.ids, id)
}

// AddRule adds an always-on rule.
func (a *AlwaysBucket) AddRule(id string, priority canonical.Priority, text string) {
	a.rules = append(a.rules, alwaysRule{id: id, priority: priority, text: text})
	a.ids = append(a.ids, id)
}

// AddDecision adds the agent constraints of a decision record.
func (a *AlwaysBucket) AddDecision(id, title string, constraints []string) {
	a.decisions = append(a.decisions, alwaysDecision{id: id, title: title, constraints: constraints})
	a.ids = append(a.ids, id)
}

// Empty reports whether the bucket has no content.
func (a *AlwaysBucket) Empty() bool {
	return len(a.sections) == 0 && len(a.rules) == 0 && len(a.decisions) == 0
}

// EntityIDs returns the sorted contributing entity IDs.
func (a *AlwaysBucket) EntityIDs() []string {
	out := append([]string{}, a.ids...)
	sort.Strings(out)
	return dedupeStrings(out)
}

// Render writes the always-on file. Sections keep insertion order, which is
// the canonical project's sorted entity order, so output is deterministic.
func (a *AlwaysBucket) Render(projectName string, frontMatter []KV) string {
	var md Markdown
	title := strings.TrimSpace(projectName)
	if title == "" {
		title = "Project instructions"
	}
	md.Heading(1, title)
	for _, s := range a.sections {
		if s.title != "" {
			md.Heading(2, s.title)
		}
		md.Paragraph(s.content)
	}
	if len(a.rules) > 0 {
		md.Heading(2, "Rules")
		ordered := append([]alwaysRule{}, a.rules...)
		sort.SliceStable(ordered, func(i, j int) bool {
			if canonical.PriorityRank(ordered[i].priority) != canonical.PriorityRank(ordered[j].priority) {
				return canonical.PriorityRank(ordered[i].priority) < canonical.PriorityRank(ordered[j].priority)
			}
			return ordered[i].id < ordered[j].id
		})
		md.BlankLine()
		for _, r := range ordered {
			md.Bullet(fmt.Sprintf("**%s** %s", strings.ToUpper(string(r.priority)), r.text))
		}
	}
	if len(a.decisions) > 0 {
		md.Heading(2, "Architecture decisions")
		md.BlankLine()
		for _, d := range a.decisions {
			md.Bullet(d.title)
			for _, c := range d.constraints {
				md.Raw("  - " + strings.TrimSpace(c) + "\n")
			}
		}
	}
	return RenderFrontMatter(frontMatter) + md.String()
}
