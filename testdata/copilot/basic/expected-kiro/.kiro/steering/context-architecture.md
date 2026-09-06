---
inclusion: always
---

# Architecture

The service is a modular monolith. HTTP handlers call services; services call repositories.
Repositories are the only code allowed to touch the database.
