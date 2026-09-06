---
title: Strict typing
priority: should
enabled: true
activation:
  type: path-scoped
  include:
    - src/**/*.ts
    - src/**/*.tsx
    - lib/**/*.go
extensions:
  claude:
    description: Strict typing
    stemma.ruleFile: api.md
---

Use explicit types at module boundaries.
