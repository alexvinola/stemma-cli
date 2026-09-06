---
title: Testing
priority: should
enabled: true
activation:
  type: path-scoped
  include:
    - web/**
extensions:
  claude:
    description: Testing
    stemma.ruleFile: frontend/testing.md
---

Use Vitest on the front end.
