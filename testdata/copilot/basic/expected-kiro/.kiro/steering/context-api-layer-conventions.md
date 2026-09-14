---
inclusion: fileMatch
fileMatchPattern:
  - src/api/**
  - src/handlers/**
---

# API layer conventions

- Validate every request body at the boundary.
- Return `application/problem+json` for errors.
