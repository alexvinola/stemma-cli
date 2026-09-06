---
title: braces
kind: other
audience: agent
activation:
  type: path-scoped
  include:
    - src/[{]name.ts
    - src/[{]name.tsx
extensions:
  github-copilot:
    stemma.instructionsFile: braces.instructions.md
---

Use explicit types.
