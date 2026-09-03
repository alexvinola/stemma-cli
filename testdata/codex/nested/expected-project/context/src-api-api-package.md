---
title: API package
kind: other
audience: agent
activation:
  type: path-scoped
  include:
    - src/api/**
extensions:
  codex:
    stemma.directory: src/api
---

Validate every request body at the boundary. Return problem+json errors.
