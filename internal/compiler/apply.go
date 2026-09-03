package compiler

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/alexvinola/stemma-cli/internal/diagnostics"
	"github.com/alexvinola/stemma-cli/internal/manifest"
	"github.com/alexvinola/stemma-cli/internal/provenance"
	"github.com/alexvinola/stemma-cli/internal/version"
	"github.com/alexvinola/stemma-cli/internal/workspace"
)

// ErrStalePlan reports that the repository changed after the plan was built.
var ErrStalePlan = errors.New("plan is stale")

// ErrBlocked reports that blocking diagnostics prevent applying.
var ErrBlocked = errors.New("blocking diagnostics prevent apply")

// ApplyOptions configures an apply.
type ApplyOptions struct {
	// Manifest is the manifest to update.
	Manifest manifest.Manifest
	// Now supplies the recorded timestamp. It is metadata only and never
	// affects generated content, hashes or planning. A zero value records no
	// timestamp, which keeps tests deterministic.
	Now time.Time
	// ManifestPath is where the updated manifest is written, inside the same
	// transaction as the generated files. Empty means "do not write it".
	ManifestPath string
}

// ApplyResult reports what an apply did.
type ApplyResult struct {
	Written     []string                 `json:"written"`
	Unchanged   []string                 `json:"unchanged"`
	Skipped     []string                 `json:"skipped"`
	Manifest    manifest.Manifest        `json:"-"`
	Diagnostics []diagnostics.Diagnostic `json:"diagnostics"`
}

// Apply writes a plan transactionally.
//
// Every destination hash recorded in the plan is re-checked first: if anything
// changed since the plan was built, nothing is written. Writes go to temporary
// files and are renamed into place; if any write fails, the files already
// replaced are restored.
func Apply(ctx context.Context, ws *workspace.Workspace, plan Plan, opts ApplyOptions) (ApplyResult, error) {
	var bag diagnostics.Bag
	res := ApplyResult{Written: []string{}, Unchanged: []string{}, Skipped: []string{}}

	if blocking := plan.Blocking(); len(blocking) > 0 {
		res.Diagnostics = blocking
		return res, fmt.Errorf("%w: %d blocking diagnostic(s)", ErrBlocked, len(blocking))
	}

	// Stale-plan protection: re-check every destination the plan inspected.
	for _, c := range plan.Changes {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		current, exists, err := ws.HashFile(c.Path)
		if err != nil {
			return res, fmt.Errorf("re-check %q: %w", c.Path, err)
		}
		var recorded string
		if exists {
			recorded = current
		}
		if (exists && c.ExistingHash != recorded) || (!exists && c.ExistingHash != "") {
			bag.Add(diagnostics.New(diagnostics.StalePlan, diagnostics.SeverityError,
				"the repository changed after this plan was created").
				WithPath(c.Path).WithTarget(string(plan.Target)).
				WithDetail("Planned state: %s. Current state: %s.",
					describeHash(c.ExistingHash), describeHash(recorded)).
				WithSuggestion("Re-run `stemma plan --target %s` and review the new plan.", plan.Target))
			res.Diagnostics = bag.Items()
			return res, fmt.Errorf("%w: %s changed since planning", ErrStalePlan, c.Path)
		}
	}

	newManifest := updateManifest(opts.Manifest, plan, opts.Now)

	tx := ws.Begin()
	writes := 0
	for _, c := range plan.Changes {
		switch c.Kind {
		case ChangeCreate, ChangeUpdate:
			if err := tx.Add(workspace.WriteOp{Path: c.Path, Content: []byte(c.Content), Mode: 0o644}); err != nil {
				return res, fmt.Errorf("queue write for %q: %w", c.Path, err)
			}
			writes++
		case ChangeUnchanged:
			res.Unchanged = append(res.Unchanged, c.Path)
		case ChangeDeleteProposed:
			// Stemma never deletes files.
			res.Skipped = append(res.Skipped, c.Path)
		case ChangeConflict:
			res.Skipped = append(res.Skipped, c.Path)
			bag.Add(diagnostics.New(diagnostics.UntrackedDestConfl, diagnostics.SeverityError,
				"conflicting file was not written").
				WithPath(c.Path).WithTarget(string(plan.Target)).WithDetail("%s", c.Reason))
			res.Diagnostics = bag.Items()
			return res, fmt.Errorf("%w: %s is in conflict", ErrBlocked, c.Path)
		default:
			return res, fmt.Errorf("%w: unknown change kind %q for %q", ErrInvariant, c.Kind, c.Path)
		}
	}

	if opts.ManifestPath != "" {
		data, err := manifest.Marshal(newManifest)
		if err != nil {
			return res, fmt.Errorf("encode manifest: %w", err)
		}
		if err := tx.Add(workspace.WriteOp{Path: opts.ManifestPath, Content: data, Mode: 0o644}); err != nil {
			return res, fmt.Errorf("queue manifest write: %w", err)
		}
		writes++
	}

	if writes > 0 {
		if err := tx.Commit(); err != nil {
			var rb *workspace.RollbackError
			if errors.As(err, &rb) {
				bag.Add(diagnostics.New(diagnostics.WriteRolledBack, diagnostics.SeverityError,
					"a write failed and the rollback was incomplete").
					WithTarget(string(plan.Target)).
					WithDetail("%s", rb.RecoveryReport).
					WithSuggestion("Recovery data is in %s.", rb.RecoveryPath))
				bag.Add(diagnostics.New(diagnostics.RecoveryDataWritten, diagnostics.SeverityError,
					"recovery data was written").
					WithPath(rb.RecoveryPath))
				res.Diagnostics = bag.Items()
				return res, err
			}
			bag.Add(diagnostics.New(diagnostics.WriteRolledBack, diagnostics.SeverityError,
				"a write failed; every change was rolled back").
				WithTarget(string(plan.Target)).WithDetail("%v", err))
			res.Diagnostics = bag.Items()
			return res, err
		}
		res.Written = tx.WrittenPaths()
	}

	res.Manifest = newManifest
	res.Diagnostics = bag.Items()
	return res, nil
}

// updateManifest records the generated state after a successful apply.
func updateManifest(m manifest.Manifest, plan Plan, now time.Time) manifest.Manifest {
	if m.Targets == nil {
		m.Targets = map[string]manifest.TargetRecord{}
	}
	rec := manifest.TargetRecord{
		ProfileHash:           plan.ProfileHash,
		ProjectHash:           plan.ProjectHash,
		CompatibilityBaseline: plan.Baseline,
		StemmaVersion:         version.Version,
		AcceptedDiagnostics:   []string{},
	}
	if len(plan.AcceptedDiagnostics) > 0 {
		rec.AcceptedDiagnostics = append([]string{}, plan.AcceptedDiagnostics...)
	} else if prev, ok := m.Targets[string(plan.Target)]; ok {
		rec.AcceptedDiagnostics = prev.AcceptedDiagnostics
	}
	if !now.IsZero() {
		rec.AppliedAt = now.UTC().Format(time.RFC3339)
	}
	entitiesFor := map[string][]string{}
	for _, mp := range plan.Mappings {
		for _, f := range mp.Files {
			entitiesFor[f] = append(entitiesFor[f], mp.EntityID)
		}
	}
	for _, c := range plan.Changes {
		if c.Kind == ChangeDeleteProposed || c.Kind == ChangeConflict {
			continue
		}
		hash := c.NewHash
		if hash == "" {
			hash = provenance.HashString(c.Content)
		}
		ents := entitiesFor[c.Path]
		if len(ents) == 0 {
			ents = c.Entities
		}
		sort.Strings(ents)
		rec.GeneratedFiles = append(rec.GeneratedFiles, manifest.GeneratedRecord{
			Path: c.Path, Hash: hash, Entities: dedupe(ents),
		})
	}
	sort.Slice(rec.GeneratedFiles, func(i, j int) bool {
		return rec.GeneratedFiles[i].Path < rec.GeneratedFiles[j].Path
	})
	m.Targets[string(plan.Target)] = rec
	m.LastTarget = string(plan.Target)
	m.ProjectHash = plan.ProjectHash
	m.StemmaVersion = version.Version
	return m
}

func dedupe(in []string) []string {
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

func describeHash(h string) string {
	if h == "" {
		return "absent"
	}
	if len(h) > 19 {
		return h[:19] + "…"
	}
	return h
}
