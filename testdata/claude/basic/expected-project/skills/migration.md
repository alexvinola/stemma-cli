---
name: migration
description: Write and apply a database migration
allowedTools:
  - bash
extensions:
  claude:
    stemma.sourceDir: migration
---

Create the migration file, then apply it with `make migrate`.
