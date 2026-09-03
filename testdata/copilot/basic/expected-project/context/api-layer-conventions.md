---
title: API layer conventions
kind: other
audience: agent
activation:
  type: path-scoped
  include:
    - src/api/**
    - src/handlers/**
extensions:
  github-copilot:
    description: API layer conventions
    excludeAgent: copilot-cli
    stemma.instructionsFile: api.instructions.md
---

- Validate every request body at the boundary.
- Return `application/problem+json` for errors.
