---
applyTo: "src/api/**,src/handlers/**"
description: API layer conventions
excludeAgent: copilot-cli
---

# API conventions

- Validate every request body at the boundary.
- Return `application/problem+json` for errors.
