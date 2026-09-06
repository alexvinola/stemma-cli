---
inclusion: fileMatch
fileMatchPattern:
  - src/api/**/*.go
  - src/handlers/**/*.go
---

# API request validation

MUST: Validate every request body at the boundary and return problem+json errors.
