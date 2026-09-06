---
title: Strict typing
kind: other
audience: agent
activation:
  type: path-scoped
  include:
    - src/**/*.ts
    - src/**/*.tsx
    - lib/**/*.go
    - app/**/*.css
    - packages/**/*.css
extensions:
  github-copilot:
    description: Strict typing
    stemma.instructionsFile: mixed.instructions.md
---

Use explicit types at module boundaries.
