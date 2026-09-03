---
title: API request validation
priority: must
enabled: true
activation:
  type: path-scoped
  include:
    - src/api/**/*.go
    - src/handlers/**/*.go
extensions:
  claude:
    description: API request validation
    stemma.ruleFile: api.md
---

Validate every request body at the boundary and return problem+json errors.
