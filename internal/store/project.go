package store

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/alexvinola/stemma-cli/internal/canonical"
	"github.com/alexvinola/stemma-cli/internal/diagnostics"
	"github.com/alexvinola/stemma-cli/internal/provenance"
	"github.com/alexvinola/stemma-cli/internal/version"
	"github.com/alexvinola/stemma-cli/internal/workspace"
)

// projectFile is the project-level metadata at .stemma/project.json. The
// entities themselves live in Markdown files beside it.
type projectFile struct {
	SchemaVersion     int                      `json:"schemaVersion"`
	ID                string                   `json:"id"`
	Name              string                   `json:"name"`
	Description       string                   `json:"description,omitempty"`
	Language          string                   `json:"language,omitempty"`
	Framework         string                   `json:"framework,omitempty"`
	ArchitectureStyle string                   `json:"architectureStyle,omitempty"`
	Targets           []canonical.TargetFormat `json:"targets"`
	TokenBudgets      canonical.TokenBudgets   `json:"tokenBudgets,omitzero"`
	Extensions        canonical.Extensions     `json:"extensions,omitempty"`
}

// provenanceFile is machine bookkeeping: where each entity came from, and the
// provider content Stemma preserved without interpreting. It is never edited
// by hand. Losing it costs byte-identical round trips, not correctness.
type provenanceFile struct {
	SchemaVersion int                              `json:"schemaVersion"`
	Entities      map[string]provenance.Provenance `json:"entities"`
	OpaqueBlocks  []canonical.OpaqueBlock          `json:"opaqueBlocks"`
}

func marshalJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// LoadProject reads the canonical project: metadata, every entity file, and
// the recorded provenance.
func LoadProject(ctx context.Context, ws *workspace.Workspace) (canonical.Project, error) {
	project, diags, err := LoadProjectWithDiagnostics(ctx, ws)
	if err != nil {
		return canonical.Project{}, err
	}
	for _, d := range diags {
		if d.Severity == diagnostics.SeverityError {
			return canonical.Project{}, fmt.Errorf("%s: %s", d.Path, d.Summary)
		}
	}
	return project, nil
}

// LoadProjectWithDiagnostics reads the project, reporting per-file problems as
// diagnostics instead of a single error.
func LoadProjectWithDiagnostics(
	ctx context.Context, ws *workspace.Workspace,
) (canonical.Project, []diagnostics.Diagnostic, error) {
	exists, err := ws.Exists(ProjectFile)
	if err != nil {
		return canonical.Project{}, nil, err
	}
	if !exists {
		return canonical.Project{}, nil, fmt.Errorf("%w at %s", ErrNoProject, ProjectFile)
	}
	f, err := ws.ReadFile(ctx, ProjectFile)
	if err != nil {
		return canonical.Project{}, nil, fmt.Errorf("read %s: %w", ProjectFile, err)
	}
	var meta projectFile
	dec := json.NewDecoder(bytes.NewReader(f.Data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&meta); err != nil {
		if looksLikeV1(f.Data) {
			return canonical.Project{}, nil, fmt.Errorf(
				"%s uses the old single-file layout (schema version 1). Entities now live in "+
					"Markdown files under .stemma/. Re-run `stemma import` to produce the new layout",
				ProjectFile)
		}
		return canonical.Project{}, nil, fmt.Errorf("%s: %w", ProjectFile, err)
	}
	if meta.SchemaVersion != version.CanonicalSchemaVersion {
		return canonical.Project{}, nil, fmt.Errorf(
			"%s declares schema version %d, this build supports %d",
			ProjectFile, meta.SchemaVersion, version.CanonicalSchemaVersion)
	}

	project := canonical.NewProject(meta.ID, meta.Name)
	project.Description = meta.Description
	project.Language = meta.Language
	project.Framework = meta.Framework
	project.ArchitectureStyle = meta.ArchitectureStyle
	project.TokenBudgets = meta.TokenBudgets
	project.Extensions = meta.Extensions
	if meta.Targets != nil {
		project.Targets = meta.Targets
	}

	var bag diagnostics.Bag
	for _, entry := range EntityDirs() {
		paths, err := EntityFiles(ctx, ws, entry.Dir)
		if err != nil {
			return canonical.Project{}, nil, err
		}
		for _, rel := range paths {
			id, ok := IDFromPath(rel)
			if !ok {
				continue
			}
			file, err := ws.ReadFile(ctx, rel)
			if err != nil {
				bag.Add(diagnostics.New(diagnostics.FileUnreadable, diagnostics.SeverityError,
					"entity file could not be read").WithPath(rel).WithDetail("%v", err))
				continue
			}
			switch entry.Type {
			case canonical.EntityContext:
				e, d := DecodeContext(id, rel, file.Data)
				bag.Extend(d)
				project.ContextDocuments = append(project.ContextDocuments, e)
			case canonical.EntityRule:
				e, d := DecodeRule(id, rel, file.Data)
				bag.Extend(d)
				project.Rules = append(project.Rules, e)
			case canonical.EntityProcedure:
				e, d := DecodeProcedure(id, rel, file.Data)
				bag.Extend(d)
				project.Procedures = append(project.Procedures, e)
			case canonical.EntitySkill:
				e, d := DecodeSkill(id, rel, file.Data)
				bag.Extend(d)
				project.Skills = append(project.Skills, e)
			case canonical.EntityAgent:
				e, d := DecodeAgent(id, rel, file.Data)
				bag.Extend(d)
				project.Agents = append(project.Agents, e)
			case canonical.EntityDecision:
				e, d := DecodeDecision(id, rel, file.Data)
				bag.Extend(d)
				project.Decisions = append(project.Decisions, e)
			}
		}
	}

	prov, err := loadProvenance(ctx, ws)
	if err != nil {
		return canonical.Project{}, nil, err
	}
	attachProvenance(&project, prov)
	project.Sort()
	return project, bag.Items(), nil
}

// looksLikeV1 detects the old all-in-one project file, so the error message can
// say what to do instead of complaining about an unknown field.
func looksLikeV1(data []byte) bool {
	return bytes.Contains(data, []byte(`"contextDocuments"`)) ||
		bytes.Contains(data, []byte(`"opaqueBlocks"`))
}

// EntityFiles lists the Markdown files in an entity directory, sorted.
func EntityFiles(ctx context.Context, ws *workspace.Workspace, dir string) ([]string, error) {
	exists, err := ws.DirExists(dir)
	if err != nil || !exists {
		return nil, err
	}
	walk, err := ws.Walk(ctx, dir)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(walk.Files))
	for _, rel := range walk.Files {
		if strings.HasSuffix(rel, ".md") {
			out = append(out, rel)
		}
	}
	sort.Strings(out)
	return out, nil
}

func loadProvenance(ctx context.Context, ws *workspace.Workspace) (provenanceFile, error) {
	out := provenanceFile{SchemaVersion: 1, Entities: map[string]provenance.Provenance{}}
	exists, err := ws.Exists(ProvenanceFile)
	if err != nil || !exists {
		return out, err
	}
	f, err := ws.ReadFile(ctx, ProvenanceFile)
	if err != nil {
		return out, fmt.Errorf("read %s: %w", ProvenanceFile, err)
	}
	var got provenanceFile
	dec := json.NewDecoder(bytes.NewReader(f.Data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&got); err != nil {
		return out, fmt.Errorf("%s: %w", ProvenanceFile, err)
	}
	if got.Entities == nil {
		got.Entities = map[string]provenance.Provenance{}
	}
	return got, nil
}

func attachProvenance(p *canonical.Project, prov provenanceFile) {
	for i := range p.ContextDocuments {
		p.ContextDocuments[i].Provenance = prov.Entities[p.ContextDocuments[i].ID]
	}
	for i := range p.Rules {
		p.Rules[i].Provenance = prov.Entities[p.Rules[i].ID]
	}
	for i := range p.Procedures {
		p.Procedures[i].Provenance = prov.Entities[p.Procedures[i].ID]
	}
	for i := range p.Skills {
		p.Skills[i].Provenance = prov.Entities[p.Skills[i].ID]
	}
	for i := range p.Agents {
		p.Agents[i].Provenance = prov.Entities[p.Agents[i].ID]
	}
	for i := range p.Decisions {
		p.Decisions[i].Provenance = prov.Entities[p.Decisions[i].ID]
	}
	p.OpaqueBlocks = prov.OpaqueBlocks
	if p.OpaqueBlocks == nil {
		p.OpaqueBlocks = []canonical.OpaqueBlock{}
	}
}

// EncodedProject is the full on-disk form of a canonical project.
type EncodedProject struct {
	// Files maps repository-relative paths to their exact content.
	Files map[string][]byte
}

// EncodeProject renders a project as the set of files that represent it.
func EncodeProject(p canonical.Project) (EncodedProject, error) {
	p.Sort()
	files := map[string][]byte{}

	meta := projectFile{
		SchemaVersion:     version.CanonicalSchemaVersion,
		ID:                p.ID,
		Name:              p.Name,
		Description:       p.Description,
		Language:          p.Language,
		Framework:         p.Framework,
		ArchitectureStyle: p.ArchitectureStyle,
		Targets:           p.Targets,
		TokenBudgets:      p.TokenBudgets,
		Extensions:        p.Extensions,
	}
	if meta.Targets == nil {
		meta.Targets = []canonical.TargetFormat{}
	}
	data, err := marshalJSON(meta)
	if err != nil {
		return EncodedProject{}, err
	}
	files[ProjectFile] = data

	add := func(id, content string) error {
		rel, err := EntityPath(id)
		if err != nil {
			return err
		}
		files[rel] = []byte(content)
		return nil
	}
	for _, e := range p.ContextDocuments {
		if err := add(e.ID, EncodeContext(e)); err != nil {
			return EncodedProject{}, err
		}
	}
	for _, e := range p.Rules {
		if err := add(e.ID, EncodeRule(e)); err != nil {
			return EncodedProject{}, err
		}
	}
	for _, e := range p.Procedures {
		if err := add(e.ID, EncodeProcedure(e)); err != nil {
			return EncodedProject{}, err
		}
	}
	for _, e := range p.Skills {
		if err := add(e.ID, EncodeSkill(e)); err != nil {
			return EncodedProject{}, err
		}
	}
	for _, e := range p.Agents {
		if err := add(e.ID, EncodeAgent(e)); err != nil {
			return EncodedProject{}, err
		}
	}
	for _, e := range p.Decisions {
		if err := add(e.ID, EncodeDecision(e)); err != nil {
			return EncodedProject{}, err
		}
	}

	prov := provenanceFile{
		SchemaVersion: 1,
		Entities:      map[string]provenance.Provenance{},
		OpaqueBlocks:  p.OpaqueBlocks,
	}
	if prov.OpaqueBlocks == nil {
		prov.OpaqueBlocks = []canonical.OpaqueBlock{}
	}
	collect := func(id string, pv provenance.Provenance) {
		if pv.SourcePath != "" || pv.SourceFormat != "" {
			prov.Entities[id] = pv
		}
	}
	for _, e := range p.ContextDocuments {
		collect(e.ID, e.Provenance)
	}
	for _, e := range p.Rules {
		collect(e.ID, e.Provenance)
	}
	for _, e := range p.Procedures {
		collect(e.ID, e.Provenance)
	}
	for _, e := range p.Skills {
		collect(e.ID, e.Provenance)
	}
	for _, e := range p.Agents {
		collect(e.ID, e.Provenance)
	}
	for _, e := range p.Decisions {
		collect(e.ID, e.Provenance)
	}
	provData, err := marshalJSON(prov)
	if err != nil {
		return EncodedProject{}, err
	}
	files[ProvenanceFile] = provData

	return EncodedProject{Files: files}, nil
}

// SaveProject writes the whole canonical project in one transaction.
//
// When prune is set, entity files that the project no longer contains are
// removed. Only Markdown files inside Stemma's own entity directories are ever
// removed, and only when the caller has authorised replacing the project.
func SaveProject(ctx context.Context, ws *workspace.Workspace, p canonical.Project, prune bool) ([]string, error) {
	encoded, err := EncodeProject(p)
	if err != nil {
		return nil, err
	}
	tx := ws.Begin()
	paths := make([]string, 0, len(encoded.Files))
	for rel := range encoded.Files {
		paths = append(paths, rel)
	}
	sort.Strings(paths)
	for _, rel := range paths {
		if err := tx.Add(workspace.WriteOp{Path: rel, Content: encoded.Files[rel], Mode: 0o644}); err != nil {
			return nil, err
		}
	}

	var stale []string
	if prune {
		for _, entry := range EntityDirs() {
			existing, err := EntityFiles(ctx, ws, entry.Dir)
			if err != nil {
				return nil, err
			}
			for _, rel := range existing {
				if _, kept := encoded.Files[rel]; kept {
					continue
				}
				if err := tx.Remove(rel); err != nil {
					return nil, err
				}
				stale = append(stale, rel)
			}
		}
	}
	sort.Strings(stale)
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return stale, nil
}
