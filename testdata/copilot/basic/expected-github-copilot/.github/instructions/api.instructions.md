---
applyTo: src/api/**,src/handlers/**
description: API layer conventions
---

# API layer conventions

- Validate every request body at the boundary.
- Return `application/problem+json` for errors.
