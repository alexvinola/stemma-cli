// Package kiro implements the Kiro adapter.
//
// Recognised paths:
//
//	.kiro/steering/*.md      steering documents (inclusion: always|fileMatch|manual|auto)
//	.kiro/skills/*/SKILL.md  skills
//	.kiro/agents/*.json      custom agents
package kiro

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/alexvinola/stemma/internal/adapters"
	"github.com/alexvinola/stemma/internal/canonical"
	"github.com/alexvinola/stemma/internal/diagnostics"
	"github.com/alexvinola/stemma/internal/discovery"
	"github.com/alexvinola/stemma/internal/globs"
	"github.com/alexvinola/stemma/internal/provenance"
)

// Directories Kiro reads.
const (
	SteeringDir = ".kiro/steering"
	SkillsDir   = ".kiro/skills"
	AgentsDir   = ".kiro/agents"
)

// Inclusion modes documented for steering files.
const (
	InclusionAlways    = "always"
	InclusionFileMatch = "fileMatch"
	InclusionManual    = "manual"
	InclusionAuto      = "auto"
)

// foundationKinds maps the documented foundation files to context kinds. The
// mapping is by file name, which is a structural fact, not an inference from
// the prose inside the file.
var foundationKinds = map[string]canonical.ContextKind{
	"product.md":   canonical.KindProduct,
	"structure.md": canonical.KindStructure,
	"tech.md":      canonical.KindTechnology,
}

// Importer converts Kiro configuration into canonical entities.
type Importer struct{}

// Format implements adapters.Importer.
func (Importer) Format() canonical.TargetFormat { return canonical.TargetKiro }

// Import implements adapters.Importer.
func (Importer) Import(ctx context.Context, in adapters.ImportInput) (adapters.ImportResult, error) {
	var bag diagnostics.Bag
	c := &adapters.ImportCtx{Provider: canonical.TargetKiro, IDs: in.IDs, Bag: &bag}
	project := canonical.NewProject("", "")

	for _, file := range in.Files {
		if err := ctx.Err(); err != nil {
			return adapters.ImportResult{}, err
		}
		switch file.Role {
		case discovery.RoleSteering:
			importSteering(c, &project, file)
		case discovery.RoleSkill:
			doc, ok := c.ParseDocument(file)
			if !ok {
				continue
			}
			project.Skills = append(project.Skills, c.SkillFromDocument(file, doc, discovery.SkillName(file.Path)))
		case discovery.RoleAgent:
			importAgent(c, &project, file)
		default:
			bag.Add(diagnostics.New(diagnostics.UnrecognizedFormat, diagnostics.SeverityWarning,
				"file matched the Kiro registry but has no importer for its role").WithPath(file.Path))
		}
	}
	project.OpaqueBlocks = append(project.OpaqueBlocks, c.Opaque...)
	return adapters.ImportResult{Project: project, Diagnostics: bag.Items()}, nil
}

// importSteering maps a steering document to a canonical context document.
func importSteering(c *adapters.ImportCtx, project *canonical.Project, file adapters.SourceFile) {
	doc, ok := c.ParseDocument(file)
	if !ok {
		return
	}
	title := adapters.TitleFor(doc, file, "name", "description")
	id := c.IDs.Allocate(canonical.EntityContext, canonical.Slug(title), file.Path)

	inclusion := InclusionAlways
	if v, has := doc.FrontMatter.String("inclusion"); has && strings.TrimSpace(v) != "" {
		inclusion = strings.TrimSpace(v)
	}
	activation := canonical.Always()
	switch inclusion {
	case InclusionAlways:
		activation = canonical.Always()
	case InclusionFileMatch:
		patterns, valid := doc.FrontMatter.StringList("fileMatchPattern")
		if !valid || len(patterns) == 0 {
			c.Bag.Add(diagnostics.New(diagnostics.InvalidFrontMatter, diagnostics.SeverityError,
				"inclusion is fileMatch but fileMatchPattern is missing or not a string list").
				WithPath(file.Path).WithEntity(id).WithPosition(doc.FrontMatter.StartLine, 1).
				WithSuggestion("Add fileMatchPattern, or change inclusion to always or manual."))
			return
		}
		for _, p := range patterns {
			if err := globs.Validate(p); err != nil {
				c.Bag.Add(diagnostics.New(diagnostics.InvalidGlob, diagnostics.SeverityError,
					"invalid fileMatchPattern").
					WithPath(file.Path).WithEntity(id).WithDetail("%v", err))
				return
			}
		}
		activation = canonical.PathScoped(patterns, nil)
	case InclusionManual, InclusionAuto:
		trigger, _ := doc.FrontMatter.String("description")
		name, _ := doc.FrontMatter.String("name")
		if name == "" {
			name = strings.TrimSuffix(path.Base(file.Path), ".md")
		}
		activation = canonical.OnDemand(strings.TrimSpace(trigger), name)
	default:
		c.Bag.Add(diagnostics.New(diagnostics.InvalidFrontMatter, diagnostics.SeverityError,
			fmt.Sprintf("unknown steering inclusion mode %q", inclusion)).
			WithPath(file.Path).WithEntity(id).WithPosition(doc.FrontMatter.StartLine, 1).
			WithDetail("Known modes are: always, fileMatch, manual, auto.").
			WithSuggestion("Fix the inclusion value, or remove it to use the documented default (always)."))
		return
	}

	content := adapters.BodyWithoutTitle(doc)
	if strings.TrimSpace(content) == "" {
		c.AddOpaque(file, string(file.Data), "steering file has no body content",
			adapters.FullSpan(file, doc), true)
		return
	}
	kind := canonical.KindOther
	if k, ok := foundationKinds[path.Base(file.Path)]; ok {
		kind = k
	} else if k := adapters.KindFromHeading(title); k != canonical.KindOther {
		kind = k
	}

	entity := canonical.ContextDocument{
		ID:         id,
		Title:      title,
		Kind:       kind,
		Content:    content,
		Audience:   canonical.AudienceAgent,
		Activation: activation,
		Provenance: c.Provenance(file, adapters.FullSpan(file, doc), provenance.DispositionParsed),
	}
	entity.Extensions.Set(string(canonical.TargetKiro), "stemma.steeringFile", path.Base(file.Path))
	entity.Extensions.Set(string(canonical.TargetKiro), "inclusion", inclusion)
	if desc, has := doc.FrontMatter.String("description"); has && strings.TrimSpace(desc) != "" {
		entity.Extensions.Set(string(canonical.TargetKiro), "description", strings.TrimSpace(desc))
	}
	if name, has := doc.FrontMatter.String("name"); has && strings.TrimSpace(name) != "" {
		entity.Extensions.Set(string(canonical.TargetKiro), "name", strings.TrimSpace(name))
	}
	c.PreserveUnknownKeys(&entity.Extensions, doc, file, id,
		"inclusion", "fileMatchPattern", "description", "name")
	project.ContextDocuments = append(project.ContextDocuments, entity)
}

// agentJSON is the subset of a Kiro agent definition Stemma understands.
// Everything else is preserved as a provider extension.
type agentJSON struct {
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Prompt       string   `json:"prompt"`
	Instructions string   `json:"instructions"`
	Tools        []string `json:"tools"`
	Model        string   `json:"model"`
}

// importAgent maps a .kiro/agents/*.json file to a canonical agent.
func importAgent(c *adapters.ImportCtx, project *canonical.Project, file adapters.SourceFile) {
	if dup := duplicateJSONKeys(file.Data); len(dup) > 0 {
		c.Bag.Add(diagnostics.New(diagnostics.DuplicateJSONKey, diagnostics.SeverityError,
			fmt.Sprintf("the agent definition repeats the key(s): %s", strings.Join(dup, ", "))).
			WithPath(file.Path).
			WithDetail("Duplicate JSON keys are ambiguous; Stemma refuses to guess which value wins.").
			WithSuggestion("Remove the duplicate key."))
		c.AddOpaque(file, string(file.Data), "the agent definition has duplicate JSON keys",
			provenance.Span{ByteStart: 0, ByteEnd: len(file.Data)}, true)
		return
	}

	var raw map[string]any
	if err := json.Unmarshal(file.Data, &raw); err != nil {
		c.Bag.Add(diagnostics.New(diagnostics.InvalidAgentJSON, diagnostics.SeverityError,
			"the agent definition is not valid JSON").
			WithPath(file.Path).WithDetail("%v", err))
		c.AddOpaque(file, string(file.Data), "the agent definition could not be parsed as JSON",
			provenance.Span{ByteStart: 0, ByteEnd: len(file.Data)}, true)
		return
	}
	var parsed agentJSON
	if err := json.Unmarshal(file.Data, &parsed); err != nil {
		c.Bag.Add(diagnostics.New(diagnostics.InvalidAgentJSON, diagnostics.SeverityError,
			"the agent definition has fields of unexpected types").
			WithPath(file.Path).WithDetail("%v", err))
		c.AddOpaque(file, string(file.Data), "the agent definition had unexpected field types",
			provenance.Span{ByteStart: 0, ByteEnd: len(file.Data)}, true)
		return
	}

	name := parsed.Name
	if strings.TrimSpace(name) == "" {
		name = strings.TrimSuffix(path.Base(file.Path), ".json")
	}
	id := c.IDs.Allocate(canonical.EntityAgent, canonical.Slug(name), file.Path)
	instructions := parsed.Prompt
	if strings.TrimSpace(instructions) == "" {
		instructions = parsed.Instructions
	}
	if strings.TrimSpace(instructions) == "" {
		c.Bag.Add(diagnostics.New(diagnostics.MissingRequired, diagnostics.SeverityError,
			"the agent definition has neither prompt nor instructions").
			WithPath(file.Path).WithEntity(id))
		c.AddOpaque(file, string(file.Data), "the agent definition carried no instructions",
			provenance.Span{ByteStart: 0, ByteEnd: len(file.Data)}, true)
		return
	}

	agent := canonical.Agent{
		ID:              id,
		Name:            name,
		Description:     parsed.Description,
		Instructions:    instructions,
		Tools:           parsed.Tools,
		ModelPreference: parsed.Model,
		Provenance: c.Provenance(file, provenance.Span{ByteStart: 0, ByteEnd: len(file.Data)},
			provenance.DispositionParsed),
	}
	agent.Extensions.Set(string(canonical.TargetKiro), "stemma.sourceFile", path.Base(file.Path))
	if parsed.Prompt != "" {
		agent.Extensions.Set(string(canonical.TargetKiro), "stemma.instructionsKey", "prompt")
	}
	known := map[string]struct{}{
		"name": {}, "description": {}, "prompt": {}, "instructions": {}, "tools": {}, "model": {},
	}
	var unknown []string
	for k := range raw {
		if _, ok := known[k]; !ok {
			unknown = append(unknown, k)
		}
	}
	sort.Strings(unknown)
	for _, k := range unknown {
		agent.Extensions.Set(string(canonical.TargetKiro), k, raw[k])
	}
	if len(unknown) > 0 {
		c.Bag.Add(diagnostics.New(diagnostics.UnknownKeysKept, diagnostics.SeverityInfo,
			fmt.Sprintf("unrecognised agent fields preserved as kiro extensions: %s",
				strings.Join(unknown, ", "))).
			WithPath(file.Path).WithEntity(id))
	}
	project.Agents = append(project.Agents, agent)
}

// duplicateJSONKeys reports repeated keys in any object of a JSON document.
func duplicateJSONKeys(data []byte) []string {
	dec := json.NewDecoder(strings.NewReader(string(data)))
	var dups []string
	seen := map[string]struct{}{}
	var walk func(prefix string) error
	walk = func(prefix string) error {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case json.Delim:
			switch t {
			case '{':
				local := map[string]struct{}{}
				for dec.More() {
					keyTok, err := dec.Token()
					if err != nil {
						return err
					}
					key, ok := keyTok.(string)
					if !ok {
						return fmt.Errorf("unexpected object key")
					}
					full := prefix + key
					if _, dup := local[key]; dup {
						if _, already := seen[full]; !already {
							seen[full] = struct{}{}
							dups = append(dups, full)
						}
					}
					local[key] = struct{}{}
					if err := walk(full + "."); err != nil {
						return err
					}
				}
				_, err := dec.Token() // closing brace
				return err
			case '[':
				for dec.More() {
					if err := walk(prefix); err != nil {
						return err
					}
				}
				_, err := dec.Token() // closing bracket
				return err
			}
		}
		return nil
	}
	if err := walk(""); err != nil {
		return nil // malformed JSON is reported by the caller's decode step
	}
	sort.Strings(dups)
	return dups
}
