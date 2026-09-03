package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/alexvinola/stemma/internal/adapters"
	"github.com/alexvinola/stemma/internal/canonical"
	"github.com/alexvinola/stemma/internal/capabilities"
	"github.com/alexvinola/stemma/internal/compiler"
	"github.com/alexvinola/stemma/internal/diagnostics"
	"github.com/alexvinola/stemma/internal/store"
)

type explainData struct {
	EntityID     string                      `json:"entityId"`
	EntityType   string                      `json:"entityType"`
	Title        string                      `json:"title"`
	Target       string                      `json:"target"`
	Mapping      *compiler.ProjectionMapping `json:"mapping"`
	Capabilities []string                    `json:"capabilityDecisions"`
	Diagnostics  []diagnostics.Diagnostic    `json:"entityDiagnostics"`
}

func runExplain(ctx context.Context, env Env, args []string) int {
	fs := newFlagSet(env, "explain")
	jsonOut := fs.Bool("json", false, "emit JSON")
	dir := fs.String("workspace", "", "repository root (default: current directory)")
	target := fs.String("target", "", "target format to explain")
	profilePath := fs.String("profile", "", "target profile to use")
	positional, code, ok := parsePositional(fs, args)
	if !ok {
		return code
	}
	if len(positional) != 1 {
		return fail(env, "explain", *jsonOut, ExitUsage,
			fmt.Errorf("explain takes exactly one entity id, for example: stemma explain rule.api-validation --target claude"), nil)
	}
	entityID := positional[0]

	plan, ws, code, err := buildPlan(ctx, env, *dir, *target, *profilePath, false)
	if err != nil {
		return fail(env, "explain", *jsonOut, code, err, nil)
	}
	project, err := store.LoadProject(ctx, ws)
	if err != nil {
		return fail(env, "explain", *jsonOut, ExitDiagnostics, err, nil)
	}

	var mapping *compiler.ProjectionMapping
	for i := range plan.Mappings {
		if plan.Mappings[i].EntityID == entityID {
			mapping = &plan.Mappings[i]
			break
		}
	}
	if mapping == nil {
		return fail(env, "explain", *jsonOut, ExitUsage,
			fmt.Errorf("no entity %q in this project; run `stemma validate` to list entity ids", entityID), nil)
	}

	title, kind := entityTitle(project, entityID)
	entityDiags := diagnostics.Filter(plan.Diagnostics, func(d diagnostics.Diagnostic) bool {
		return d.EntityID == entityID
	})
	data := explainData{
		EntityID:     entityID,
		EntityType:   string(mapping.EntityType),
		Title:        title,
		Target:       string(plan.Target),
		Mapping:      mapping,
		Capabilities: capabilityDecisions(plan, *mapping),
		Diagnostics:  entityDiags,
	}
	_ = kind

	if *jsonOut {
		if werr := WriteJSON(env, NewEnvelope("explain", ExitOK, entityDiags, data)); werr != nil {
			return ExitInternal
		}
		return ExitOK
	}

	fmt.Fprintf(env.Stdout, "%s  (%s)\n", entityID, mapping.EntityType)
	if title != "" {
		fmt.Fprintf(env.Stdout, "  title:        %s\n", SanitizeLine(title))
	}
	fmt.Fprintf(env.Stdout, "  target:       %s\n", plan.Target)
	fmt.Fprintf(env.Stdout, "  activation:   %s\n", adapters.ScopeLabel(mapping.Activation))
	if mapping.Source.SourcePath != "" {
		fmt.Fprintf(env.Stdout, "  imported from:%s (%s, %s)\n",
			" "+mapping.Source.SourcePath, mapping.Source.SourceFormat, mapping.Source.Disposition)
		if mapping.Source.Span.LineStart > 0 {
			fmt.Fprintf(env.Stdout, "                lines %d-%d\n",
				mapping.Source.Span.LineStart, mapping.Source.Span.LineEnd)
		}
	}
	if mapping.Override != nil {
		fmt.Fprintf(env.Stdout, "  profile:      override applied%s\n", describeOverride(*mapping.Override))
	} else {
		fmt.Fprintf(env.Stdout, "  profile:      no override\n")
	}
	fmt.Fprintf(env.Stdout, "  outcome:      %s\n", mapping.Outcome)
	if len(mapping.Files) > 0 {
		fmt.Fprintf(env.Stdout, "  destination:  %s\n", strings.Join(mapping.Files, ", "))
	} else {
		fmt.Fprintf(env.Stdout, "  destination:  (none)\n")
	}
	fmt.Fprintf(env.Stdout, "  reason:       %s\n", SanitizeLine(mapping.Explanation))
	fmt.Fprintf(env.Stdout, "  est. tokens:  ~%s (approximate)\n", FormatTokens(mapping.Tokens))
	fmt.Fprintf(env.Stdout, "\nCapability decisions for %s\n", plan.Target)
	for _, c := range data.Capabilities {
		fmt.Fprintf(env.Stdout, "  %s\n", c)
	}
	PrintDiagnostics(env.Stdout, entityDiags, true)
	return ExitOK
}

func entityTitle(p canonical.Project, id string) (string, canonical.EntityType) {
	for _, e := range p.ContextDocuments {
		if e.ID == id {
			return e.Title, canonical.EntityContext
		}
	}
	for _, e := range p.Rules {
		if e.ID == id {
			return e.Title, canonical.EntityRule
		}
	}
	for _, e := range p.Procedures {
		if e.ID == id {
			return e.Name, canonical.EntityProcedure
		}
	}
	for _, e := range p.Skills {
		if e.ID == id {
			return e.Name, canonical.EntitySkill
		}
	}
	for _, e := range p.Agents {
		if e.ID == id {
			return e.Name, canonical.EntityAgent
		}
	}
	for _, e := range p.Decisions {
		if e.ID == id {
			return e.Title, canonical.EntityDecision
		}
	}
	for _, e := range p.OpaqueBlocks {
		if e.ID == id {
			return "preserved content from " + e.SourcePath, canonical.EntityOpaque
		}
	}
	return "", ""
}

func describeOverride(o adapters.AppliedOverride) string {
	var parts []string
	if o.Include != nil {
		parts = append(parts, fmt.Sprintf("include=%t", *o.Include))
	}
	if o.Activation != nil {
		parts = append(parts, "activation="+string(o.Activation.Type))
	}
	if o.Directory != "" {
		parts = append(parts, "directory="+o.Directory)
	}
	if o.Filename != "" {
		parts = append(parts, "filename="+o.Filename)
	}
	if o.AcceptLossy {
		parts = append(parts, "acceptLossy")
	}
	if o.ContentOverride {
		parts = append(parts, "contentOverride")
	}
	if len(parts) == 0 {
		return ""
	}
	return " (" + strings.Join(parts, ", ") + ")"
}

// capabilityDecisions explains which provider capabilities drove the outcome.
func capabilityDecisions(plan compiler.Plan, m compiler.ProjectionMapping) []string {
	caps := capabilities.MustFor(plan.Target)
	out := []string{
		fmt.Sprintf("always-on instructions:      %s", yesNo(caps.AlwaysOn)),
		fmt.Sprintf("path-scoped instructions:    %s", yesNo(caps.PathScoped)),
		fmt.Sprintf("include glob patterns:       %s", yesNo(caps.IncludeGlobs)),
		fmt.Sprintf("exclude glob patterns:       %s", yesNo(caps.ExcludeGlobs)),
		fmt.Sprintf("multiple include patterns:   %s", yesNo(caps.MultipleIncludePatterns)),
		fmt.Sprintf("directory-scoped delivery:   %s", yesNo(caps.DirectoryScoped)),
		fmt.Sprintf("native skills:               %s", yesNo(caps.NativeSkills)),
		fmt.Sprintf("native specialist agents:    %s", yesNo(caps.NativeAgents)),
		fmt.Sprintf("native procedures/prompts:   %s", yesNo(caps.NativeProcedures)),
	}
	if m.Activation.Type == canonical.ActivationPathScoped && len(m.Activation.Exclude) > 0 && !caps.ExcludeGlobs {
		out = append(out, "this entity uses exclude patterns, which this provider cannot express")
	}
	return out
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
