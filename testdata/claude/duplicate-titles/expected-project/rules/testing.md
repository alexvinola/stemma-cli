---
title: Testing
priority: should
enabled: true
activation:
  type: path-scoped
  include:
    - api/**
extensions:
  claude:
    description: Testing
    stemma.ruleFile: backend/testing.md
---

Use go test on the back end.
