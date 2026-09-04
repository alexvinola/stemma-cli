package compiler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/alexvinola/stemma-cli/internal/adapters"
	"github.com/alexvinola/stemma-cli/internal/canonical"
	"github.com/alexvinola/stemma-cli/internal/diagnostics"
	"github.com/alexvinola/stemma-cli/internal/manifest"
	"github.com/alexvinola/stemma-cli/internal/optimizer"
	"github.com/alexvinola/stemma-cli/internal/profiles"
	"github.com/alexvinola/stemma-cli/internal/provenance"
	"github.com/alexvinola/stemma-cli/internal/tokenestimate"
	"github.com/alexvinola/stemma-cli/internal/version"
	"github.com/alexvinola/stemma-cli/internal/workspace"
)

// ChangeKind classifies what applying a plan would do to one file.
type ChangeKind string

const (
	// ChangeCreate: the file does not exist yet.
	ChangeCreate ChangeKind = "create"
	// ChangeUpdate: Stemma generated the file and it needs new content.
	ChangeUpdate ChangeKind = "update"
	// ChangeUnchanged: the file already has exactly the generated content.
	ChangeUnchanged ChangeKind = "unchanged"
	// ChangeDeleteProposed: Stemma generated the file before but no longer
	// produces it. Stemma never deletes files; it only reports these.
	ChangeDeleteProposed ChangeKind = "delete-proposed"
	// ChangeConflict: the file exists with content Stemma did not write, or was
	// modified after Stemma wrote it.
	ChangeConflict ChangeKind = "conflict"
)

// Change is one planned file operation.
type Change struct {
	Path ChangePath `json:"path"`
	Kind ChangeKind `json:"kind"`
	// ExistingHash is the digest of the file on disk, empty when absent.
	ExistingHash string `json:"existingHash,omitempty"`
	// NewHash is the digest of the content Stemma would write.
	NewHash string `json:"newHash,omitempty"`
	// TrackedHash is the digest recorded in the manifest, when tracked.
	TrackedHash string `json:"trackedHash,omitempty"`
	// Content is the exact text Stemma would write.
	Content string `json:"content,omitempty"`
	// ReusedSource reports that the original bytes were re-emitted verbatim.
	ReusedSource bool `json:"reusedSource,omitempty"`
	// Entities lists the canonical entities that contributed to the file.
	Entities []string `json:"entities"`
	// Reason explains a conflict or a proposed deletion.
	Reason string `json:"reason,omitempty"`
}

// ChangePath is a repository-relative path.
type ChangePath = string

// Plan is a reviewed, replayable compilation result.
type Plan struct {
	SchemaVersion int                    `json:"schemaVersion"`
	StemmaVersion string                 `json:"stemmaVersion"`
	Baseline      string                 `json:"compatibilityBaseline"`
	Target        canonical.TargetFormat `json:"target"`
	ProjectHash   string                 `json:"projectHash"`
	ProfileHash   string                 `json:"profileHash"`
	// AcceptedDiagnostics are the fingerprints the target profile accepts.
	AcceptedDiagnostics []string                 `json:"acceptedDiagnostics"`
	Changes             []Change                 `json:"changes"`
	Diagnostics         []diagnostics.Diagnostic `json:"diagnostics"`
	Mappings            []ProjectionMapping      `json:"mappings"`
	TokenReport         tokenestimate.Report     `json:"tokenReport"`
	Outcomes            map[string]int           `json:"outcomes"`
}

// PlanOptions configures planning.
type PlanOptions struct {
	Target  canonical.TargetFormat
	Profile profiles.Profile
	// Manifest is the current repository manifest.
	Manifest manifest.Manifest
	// AdoptUntracked accepts overwriting existing files that Stemma has never
	// written. Without it such files are reported as conflicts.
	AdoptUntracked bool
	// Estimator overrides the token estimator.
	Estimator tokenestimate.Estimator
}

// BuildPlan compiles a target and classifies the resulting file changes.
//
// It reads the workspace but never writes to it.
func BuildPlan(
	ctx context.Context, ws *workspace.Workspace, project canonical.Project, opts PlanOptions,
) (Plan, error) {
	originals, err := readOriginals(ctx, ws, project)
	if err != nil {
		return Plan{}, err
	}
	result, err := Compile(ctx, project, CompileOptions{
		Target:    opts.Target,
		Profile:   opts.Profile,
		Estimator: opts.Estimator,
		Originals: originals,
		Optimizer: optimizer.DefaultOptions(),
	})
	if err != nil {
		return Plan{}, err
	}

	projectHash, err := canonical.Hash(project)
	if err != nil {
		return Plan{}, err
	}
	profileHash, err := profiles.Hash(opts.Profile)
	if err != nil {
		return Plan{}, err
	}

	accepted := append([]string{}, opts.Profile.AcceptedDiagnostics...)
	sort.Strings(accepted)
	if accepted == nil {
		accepted = []string{}
	}
	plan := Plan{
		AcceptedDiagnostics: accepted,
		SchemaVersion:       version.PlanSchemaVersion,
		StemmaVersion:       version.Version,
		Baseline:            version.CompatibilityBaseline,
		Target:              opts.Target,
		ProjectHash:         projectHash,
		ProfileHash:         profileHash,
		Mappings:            result.Mappings,
		TokenReport:         result.TokenReport,
	}

	var bag diagnostics.Bag
	bag.Extend(result.Diagnostics)

	generated := map[string]struct{}{}
	for _, f := range result.Files {
		generated[f.Path] = struct{}{}
		change, diags := classify(ws, f, opts)
		bag.Extend(diags)
		plan.Changes = append(plan.Changes, change)
	}

	// Files Stemma generated previously but no longer produces.
	for _, tracked := range opts.Manifest.TrackedPaths(string(opts.Target)) {
		if _, still := generated[tracked]; still {
			continue
		}
		existing, exists, err := ws.HashFile(tracked)
		if err != nil || !exists {
			continue
		}
		plan.Changes = append(plan.Changes, Change{
			Path:         tracked,
			Kind:         ChangeDeleteProposed,
			ExistingHash: existing,
			Entities:     []string{},
			Reason:       "Stemma generated this file previously but no longer produces it.",
		})
		bag.Add(diagnostics.New(diagnostics.DeleteProposed, diagnostics.SeverityInfo,
			"a previously generated file is no longer produced").
			WithPath(tracked).WithTarget(string(opts.Target)).
			WithDetail("Stemma never deletes files. Review the file and remove it yourself if it is stale.").
			WithSuggestion("Delete %s manually if it is no longer wanted.", tracked))
	}

	sort.Slice(plan.Changes, func(i, j int) bool { return plan.Changes[i].Path < plan.Changes[j].Path })
	plan.Diagnostics = bag.Items()
	plan.Outcomes = map[string]int{}
	for outcome, n := range OutcomeCounts(result.Mappings) {
		plan.Outcomes[string(outcome)] = n
	}
	return plan, nil
}

// classify decides what applying a generated file would do.
func classify(ws *workspace.Workspace, f GeneratedFile, opts PlanOptions) (Change, []diagnostics.Diagnostic) {
	var diags []diagnostics.Diagnostic
	newHash := provenance.HashBytes(f.Content)
	change := Change{
		Path:         f.Path,
		NewHash:      newHash,
		Content:      f.Text,
		ReusedSource: f.ReusedSource,
		Entities:     f.Entities,
	}
	existingHash, exists, err := ws.HashFile(f.Path)
	if err != nil {
		change.Kind = ChangeConflict
		change.Reason = err.Error()
		code := diagnostics.FileUnreadable
		summary := "destination could not be inspected"
		suggestion := ""
		if errors.Is(err, workspace.ErrSymlink) {
			code = diagnostics.SymlinkRejected
			summary = "destination is a symbolic link"
			suggestion = "Stemma never writes through a symlink. Replace it with a regular file, " +
				"or point the target profile somewhere else."
		}
		d := diagnostics.New(code, diagnostics.SeverityError, summary).
			WithPath(f.Path).WithTarget(string(opts.Target)).WithDetail("%v", err)
		if suggestion != "" {
			d = d.WithSuggestion("%s", suggestion)
		}
		diags = append(diags, d)
		return change, diags
	}
	trackedHash, tracked := opts.Manifest.Tracked(string(opts.Target), f.Path)
	change.ExistingHash = existingHash
	change.TrackedHash = trackedHash

	switch {
	case !exists:
		change.Kind = ChangeCreate
	case existingHash == newHash:
		change.Kind = ChangeUnchanged
	case tracked && trackedHash == existingHash:
		change.Kind = ChangeUpdate
	case tracked && trackedHash != existingHash:
		change.Kind = ChangeConflict
		change.Reason = "the file was modified after Stemma generated it"
		diags = append(diags, diagnostics.New(diagnostics.UntrackedDestConfl, diagnostics.SeverityError,
			"destination was modified after Stemma generated it").
			WithPath(f.Path).WithTarget(string(opts.Target)).
			WithDetail("Applying would discard those edits.").
			WithSuggestion("Move the edits into .stemma/project.json, or delete the file and re-run."))
	case opts.AdoptUntracked:
		change.Kind = ChangeUpdate
		change.Reason = "existing file adopted with --adopt-untracked"
	default:
		change.Kind = ChangeConflict
		change.Reason = "the file exists but was not generated by Stemma"
		diags = append(diags, diagnostics.New(diagnostics.UntrackedDestConfl, diagnostics.SeverityError,
			"destination exists but is not tracked by Stemma").
			WithPath(f.Path).WithTarget(string(opts.Target)).
			WithDetail("Stemma refuses to overwrite a file it has never written.").
			WithSuggestion("Review the file, then re-run with --adopt-untracked to let Stemma own it."))
	}
	return change, diags
}

// readOriginals loads the current content of files the project was imported
// from, so unchanged files can be re-emitted byte-for-byte.
func readOriginals(
	ctx context.Context, ws *workspace.Workspace, project canonical.Project,
) (map[string]adapters.SourceFile, error) {
	paths := map[string]struct{}{}
	for path := range SourceIndex(project) {
		paths[path] = struct{}{}
	}
	ordered := make([]string, 0, len(paths))
	for p := range paths {
		ordered = append(ordered, p)
	}
	sort.Strings(ordered)

	out := make(map[string]adapters.SourceFile, len(ordered))
	for _, p := range ordered {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		f, err := ws.ReadFile(ctx, p)
		if err != nil {
			continue // the source no longer exists; regeneration handles it
		}
		out[p] = adapters.SourceFile{Path: f.Path, Data: f.Data, Hash: f.Hash, Mode: f.Mode}
	}
	return out, nil
}

// HasChanges reports whether applying the plan would modify the repository.
func (p Plan) HasChanges() bool {
	for _, c := range p.Changes {
		if c.Kind == ChangeCreate || c.Kind == ChangeUpdate || c.Kind == ChangeConflict {
			return true
		}
	}
	return false
}

// Blocking returns the diagnostics that prevent apply.
func (p Plan) Blocking() []diagnostics.Diagnostic {
	return diagnostics.Filter(p.Diagnostics, func(d diagnostics.Diagnostic) bool { return d.Blocking })
}

// Writable returns the changes that apply would perform.
func (p Plan) Writable() []Change {
	var out []Change
	for _, c := range p.Changes {
		if c.Kind == ChangeCreate || c.Kind == ChangeUpdate {
			out = append(out, c)
		}
	}
	return out
}

// CountByKind counts changes per kind.
func (p Plan) CountByKind() map[ChangeKind]int {
	out := map[ChangeKind]int{
		ChangeCreate: 0, ChangeUpdate: 0, ChangeUnchanged: 0,
		ChangeDeleteProposed: 0, ChangeConflict: 0,
	}
	for _, c := range p.Changes {
		out[c.Kind]++
	}
	return out
}

// MarshalPlan renders a plan as deterministic JSON.
func MarshalPlan(p Plan) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(p); err != nil {
		return nil, fmt.Errorf("encode plan: %w", err)
	}
	return buf.Bytes(), nil
}

// UnmarshalPlan decodes a saved plan.
func UnmarshalPlan(data []byte) (Plan, error) {
	var p Plan
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return Plan{}, fmt.Errorf("decode plan: %w", err)
	}
	// One document per plan file. Without this, content appended after the
	// first document is accepted and silently ignored.
	if dec.More() {
		return Plan{}, fmt.Errorf("%w: plan file contains more than one JSON document", ErrPlanRejected)
	}
	// Decoding is the only way a plan enters from outside, so the structural
	// checks live here rather than at each call site, where one caller could
	// forget them.
	if err := VerifyPlanStructure(p); err != nil {
		return Plan{}, err
	}
	return p, nil
}
