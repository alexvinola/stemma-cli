---
inclusion: fileMatch
fileMatchPattern: src/api/**
---

# Controllers never touch the database

MUST: Controllers must call a service; only repositories may touch the database.

> Scope note: this document does not apply to src/api/**/*_test.go.
