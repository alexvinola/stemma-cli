# Fixture

## Architecture

Handlers call services; services call repositories. Only repositories touch the database.

## Commands

- `make build` compiles the service.
- `make test` runs the unit tests.

## Rules

- **SHOULD** Run `gofmt -w .` before committing.
