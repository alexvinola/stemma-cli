---
paths:
  - src/api/**
---

# Controllers never touch the database

Controllers must call a service; only repositories may touch the database.

> Scope note: this rule does not apply to src/api/**/*_test.go.
