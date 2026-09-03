---
title: Architecture
kind: architecture
audience: agent
activation:
  type: always
---

Handlers call services; services call repositories. Only repositories touch the database.
