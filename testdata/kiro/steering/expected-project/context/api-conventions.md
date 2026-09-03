---
title: API conventions
kind: other
audience: agent
activation:
  type: path-scoped
  include:
    - src/api/**
    - src/handlers/**
extensions:
  kiro:
    inclusion: fileMatch
    stemma.steeringFile: api.md
---

Validate every request body at the boundary.
