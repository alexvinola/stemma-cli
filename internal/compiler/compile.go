// Package compiler turns a canonical project into provider files.
//
// Compilation is pure: it never reads or writes the filesystem, never prints
// and never exits. Filesystem effects live in Plan and Apply, which use the
// workspace layer.
package compiler

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"unicode/utf8"

	"github.com/alexvinola/stemma/internal/adapters"
	"github.com/alexvinola/stemma/internal/adapters/registry"
	"github.com/alexvinola/stemma/internal/canonical"
	"github.com/alexvinola/stemma/internal/capabilities"
	"github.com/alexvinola/stemma/internal/diagnostics"
	"github.com/alexvinola/stemma/internal/optimizer"
	"github.com/alexvinola/stemma/internal/profiles"
	"github.com/alexvinola/stemma/internal/tokenestimate"
	"github.com/alexvinola/stemma/internal/workspace"
)

// GeneratedFile is a file the compiler wants written.
type GeneratedFile = adapters.GeneratedFile

// ProjectionMapping explains how an entity reached a target.
type ProjectionMapping = adapters.ProjectionMapping

// Outcome is a projection outcome.
type Outcome = adapters.Outcome

// ErrTargetUnavailable reports a target that Stemma declares but cannot compile.
var ErrTargetUnavailable = errors.New("target unavailable")

// ErrInvariant reports a violated compiler invariant. Reaching it is a bug.
var ErrInvariant = errors.New("compiler invariant violated")

// CompileOptions configures one compilation.
type CompileOptions struct {
	Target  canonical.TargetFormat
	Profile profiles.Profile
	// Estimator overrides the default token estimator.
	Estimator tokenestimate.Estimator
	// Originals holds the current content of the target's existing files, so
	// unchanged files can be re-emitted byte-for-byte.
	Originals map[string]adapters.SourceFile
	// Optimizer selects the optimization passes.
	Optimizer optimizer.Options
}

// CompileResult is the outcome of compiling one target.
type CompileResult struct {
	Target       canonical.TargetFormat    `json:"target"`
	Files        []GeneratedFile           `json:"files"`
	Diagnostics  []diagnostics.Diagnostic  `json:"diagnostics"`
	Mappings     []ProjectionMapping       `json:"mappings"`
	TokenReport  tokenestimate.Report      `json:"tokenReport"`
	Capabilities capabilities.Capabilities `json:"capabilities"`
}

// Compile projects a canonical project onto a target.
//
// It returns an error only for conditions that make compilation impossible
// (an unavailable target, a cancelled context, or a violated invariant).
// Everything caused by user input is reported as a diagnostic.
func Compile(ctx context.Context, project canonical.Project, opts CompileOptions) (CompileResult, error) {
	caps, known := capabilities.For(opts.Target)
	if !known {
		return CompileResult{}, fmt.Errorf("%w: %q is not a known target", ErrTargetUnavailable, opts.Target)
	}
	if !caps.Available {
		return CompileResult{}, fmt.Errorf("%w: %q", ErrTargetUnavailable, opts.Target)
	}
	exporter, ok := registry.Exporter(opts.Target)
	if !ok {
		return CompileResult{}, fmt.Errorf("%w: %q has no exporter in this build", ErrTargetUnavailable, opts.Target)
	}
	if opts.Profile.Target == "" {
		opts.Profile = profiles.Default(opts.Target)
	}
	if opts.Profile.Target != opts.Target {
		return CompileResult{}, fmt.Errorf("profile targets %q but compilation targets %q",
			opts.Profile.Target, opts.Target)
	}

	var bag diagnostics.Bag
	bag.Extend(canonical.Validate(project))
	bag.Extend(profiles.Validate(opts.Profile, project, ""))
	if len(project.Targets) > 0 && !project.HasTarget(opts.Target) {
		bag.Add(diagnostics.New(diagnostics.TargetNotEnabled, diagnostics.SeverityWarning,
			fmt.Sprintf("target %q is not listed in the canonical project", opts.Target)).
			WithTarget(string(opts.Target)).
			WithDetail("Compilation proceeds, but `stemma check --all` will not verify this target.").
			WithSuggestion("Add %q to \"targets\" in .stemma/project.json.", opts.Target))
	}

	opt := optimizer.Run(project, opts.Optimizer)
	bag.Extend(opt.Diagnostics)

	tokens := tokenestimate.NewBuilder(opts.Estimator)
	for _, d := range project.ContextDocuments {
		if d.Activation.Type == canonical.ActivationAlways && canonical.IsEnabled(d.Enabled) {
			tokens.AddSourceAlwaysOn(d.Content)
		}
	}
	for _, r := range project.Rules {
		if r.Activation.Type == canonical.ActivationAlways && r.Enabled {
			tokens.AddSourceAlwaysOn(r.Instruction)
		}
	}

	in := adapters.ExportInput{
		Project:      opt.Project,
		Profile:      opts.Profile,
		Capabilities: caps,
		Tokens:       tokens,
		Originals:    opts.Originals,
		SourceIndex:  SourceIndex(opt.Project),
	}
	out, err := exporter.Export(ctx, in)
	if err != nil {
		return CompileResult{}, fmt.Errorf("export %s: %w", opts.Target, err)
	}
	bag.Extend(out.Diagnostics)

	mappings := append([]ProjectionMapping{}, out.Mappings...)
	for _, d := range opt.Dropped {
		mappings = append(mappings, ProjectionMapping{
			EntityID:    d.ID,
			EntityType:  d.Type,
			Target:      opts.Target,
			Outcome:     adapters.OutcomeSkipped,
			Files:       []string{},
			Diagnostics: []string{},
			Explanation: "Removed by the deduplication pass: " + d.Reason + ".",
			Activation:  canonical.DocumentationOnly(),
		})
	}
	adapters.SortMappings(mappings)

	files := append([]GeneratedFile{}, out.Files...)
	adapters.SortFiles(files)
	for i := range files {
		if err := validateGenerated(files[i]); err != nil {
			return CompileResult{}, fmt.Errorf("%w: %v", ErrInvariant, err)
		}
		tokens.AddGeneratedFile(files[i].Text)
	}

	report := tokens.Build()
	bag.Extend(optimizer.BudgetDiagnostics(project.TokenBudgets, report, string(opts.Target)))

	result := CompileResult{
		Target:       opts.Target,
		Files:        files,
		Mappings:     mappings,
		TokenReport:  report,
		Capabilities: caps,
	}
	if err := checkExhaustive(project, mappings); err != nil {
		return CompileResult{}, err
	}
	result.Diagnostics = diagnostics.Accept(bag.Items(), opts.Profile.AcceptedDiagnostics)
	diagnostics.Sort(result.Diagnostics)
	return result, nil
}

// validateGenerated enforces the invariants every generated file must satisfy.
func validateGenerated(f GeneratedFile) error {
	clean, err := workspace.NormalizeRel(f.Path)
	if err != nil {
		return fmt.Errorf("generated path %q is unsafe: %w", f.Path, err)
	}
	if clean != f.Path {
		return fmt.Errorf("generated path %q is not in normalized form (%q)", f.Path, clean)
	}
	if !utf8.Valid(f.Content) {
		return fmt.Errorf("generated file %q is not valid UTF-8", f.Path)
	}
	if string(f.Content) != f.Text {
		return fmt.Errorf("generated file %q has inconsistent Content and Text fields", f.Path)
	}
	return nil
}

// checkExhaustive verifies that every canonical entity received exactly one
// projection outcome for the target.
func checkExhaustive(project canonical.Project, mappings []ProjectionMapping) error {
	count := map[string]int{}
	for _, m := range mappings {
		if !adapters.KnownOutcome(m.Outcome) {
			return fmt.Errorf("%w: entity %q has unknown outcome %q", ErrInvariant, m.EntityID, m.Outcome)
		}
		count[m.EntityID]++
	}
	var missing, duplicated []string
	for _, e := range project.Entities() {
		switch count[e.ID] {
		case 1:
		case 0:
			missing = append(missing, e.ID)
		default:
			duplicated = append(duplicated, e.ID)
		}
	}
	for _, blk := range project.OpaqueBlocks {
		switch count[blk.ID] {
		case 1:
		case 0:
			missing = append(missing, blk.ID)
		default:
			duplicated = append(duplicated, blk.ID)
		}
	}
	sort.Strings(missing)
	sort.Strings(duplicated)
	if len(missing) > 0 {
		return fmt.Errorf("%w: no projection outcome for %v", ErrInvariant, missing)
	}
	if len(duplicated) > 0 {
		return fmt.Errorf("%w: more than one projection outcome for %v", ErrInvariant, duplicated)
	}
	return nil
}

// SourceIndex maps each source path to the entities imported from it.
func SourceIndex(p canonical.Project) map[string][]string {
	index := map[string][]string{}
	add := func(path, id string) {
		if path == "" {
			return
		}
		index[path] = append(index[path], id)
	}
	for _, e := range p.ContextDocuments {
		add(e.Provenance.SourcePath, e.ID)
	}
	for _, e := range p.Rules {
		add(e.Provenance.SourcePath, e.ID)
	}
	for _, e := range p.Procedures {
		add(e.Provenance.SourcePath, e.ID)
	}
	for _, e := range p.Skills {
		add(e.Provenance.SourcePath, e.ID)
	}
	for _, e := range p.Agents {
		add(e.Provenance.SourcePath, e.ID)
	}
	for _, e := range p.Decisions {
		add(e.Provenance.SourcePath, e.ID)
	}
	for k := range index {
		sort.Strings(index[k])
	}
	return index
}

// OutcomeCounts summarises mappings by outcome.
func OutcomeCounts(mappings []ProjectionMapping) map[Outcome]int {
	out := map[Outcome]int{
		adapters.OutcomeExact:   0,
		adapters.OutcomeAdapted: 0,
		adapters.OutcomeLossy:   0,
		adapters.OutcomeBlocked: 0,
		adapters.OutcomeSkipped: 0,
	}
	for _, m := range mappings {
		out[m.Outcome]++
	}
	return out
}
