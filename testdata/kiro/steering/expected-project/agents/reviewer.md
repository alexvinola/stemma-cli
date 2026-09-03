---
name: reviewer
description: Reviews pull requests for API breakage
tools:
  - read
  - grep
extensions:
  kiro:
    resources:
      - "file://src/api"
    stemma.instructionsKey: prompt
    stemma.sourceFile: reviewer.json
---

Review diffs for backwards-incompatible API changes.
