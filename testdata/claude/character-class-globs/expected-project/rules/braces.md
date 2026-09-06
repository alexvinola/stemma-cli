---
title: braces
priority: should
enabled: true
activation:
  type: path-scoped
  include:
    - src/[{]name.ts
    - src/[{]name.tsx
extensions:
  claude:
    stemma.ruleFile: braces.md
---

Use explicit types.
