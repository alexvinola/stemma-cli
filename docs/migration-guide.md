# Reviewing a v0.5.0 migration

Check `stemma version` and the plan for the installed build. These naming
examples describe v0.5.0; v0.4.0 cross-provider outputs used canonical-ID
filenames and skill directories more often. The command interface is unchanged.

## Copilot to Claude

For this source `.github/instructions/python.instructions.md`:

```markdown
---
applyTo: "**/*.py"
description: Python conventions
---

Use black.
```

Stemma writes `.claude/rules/python.md`:

```markdown
---
paths:
  - "**/*.py"
description: Python conventions
---

# Python conventions

Use black.
```

This is Claude's native path-scoped rule mechanism. General always-on context
goes to `CLAUDE.md`. The canonical ID remains `context.python-conventions`:
the description names the internal entity, while the safe source filename
names its destination.

A source `.github/skills/review/SKILL.md` with `name: review`, description
`Review a change` and this body:

```markdown
Review the change using [Python conventions](../../instructions/python.instructions.md).
```

becomes `.claude/skills/review/SKILL.md`, with `name: review`, the same description,
a generated `# review` heading and the unchanged body. The generated skill's
front matter and directory agree, so its invocation is `/review`. The unchanged
relative link still points at the previous instructions layout; it does not
reach `.claude/rules/python.md`.

Explicit profile destinations take precedence, followed by the target's own
recorded names and safe, unambiguous source names. Unsafe or colliding derived
names fall back to the complete canonical ID with `STEMMA3702`. A skill renamed
by that fallback or a profile has an `adapted` mapping and reports the projected
invocation name. See the full [naming policy](provider-compatibility.md#generated-names-and-destination-collisions).

## An explicit reviewed adjustment

For exactly the inputs above, save this as `.stemma/profiles/claude.json`:

```json
{
  "schemaVersion": 1,
  "target": "claude",
  "overrides": {
    "context.python-conventions": {
      "directory": ".claude/rules",
      "filename": "python-style.md"
    },
    "skill.review": {
      "directory": ".claude/skills/team-review",
      "filename": "SKILL.md",
      "contentOverride": "Review the change using [Python conventions](../../rules/python-style.md)."
    }
  },
  "acceptedDiagnostics": []
}
```

Use actual entity IDs from your own plan. This generates
`.claude/rules/python-style.md` and `.claude/skills/team-review/SKILL.md`.
The invocation becomes `/team-review`, and the skill mapping is `adapted`.
`contentOverride` explicitly replaces the whole target-facing body and emits
`STEMMA3601`; it is not automatic link rewriting. The reviewed link reaches the
generated rule. The profile was validated, applied from a saved plan and checked
against the v0.5.0 release fixes.

```bash
stemma import --from github-copilot --targets claude
# Add the profile after import and review the canonical entity IDs.
stemma validate
stemma plan --target claude --explain
stemma plan --target claude --output-plan reviewed-plan.json
stemma apply --plan reviewed-plan.json --yes
stemma check --target claude
```

Importing `SKILL.md` does not copy the entire skill package. Templates, reference
documents and scripts remain outside the import. A destination profile does
not copy them either. Paths and provider names embedded in body text are not
globally rewritten. A successful plan/check confirms the generated outputs,
not the existence of every referenced resource or identical agent behavior.
Keep GitHub infrastructure such as `.github/workflows` distinct from agent
configuration; do not replace every `.github` string with `.claude`.

## Before using the destination

- Compare source and destination paths, scoped activation and invocation names.
- Inspect generated bodies and mappings, including collision/fallback notices.
- Check every referenced resource from its new location; retain required source
  resources until dependencies have been reviewed and adjusted.
- Keep target-specific adjustments in canonical entities or profiles. Review
  generated output before using the destination agent.

Provider examples were checked against the official
[Claude rules](https://code.claude.com/docs/en/memory#organize-rules-with-clauderules),
[Claude skills](https://code.claude.com/docs/en/skills#how-a-skill-gets-its-command-name)
and [Agent Skills specification](https://agentskills.io/specification) on
2026-10-06. The [reference/resource investigation](embedded-references.md)
records the separate v0.6.0 implementation scope in
[issue #68](https://github.com/alexvinola/stemma-cli/issues/68).
