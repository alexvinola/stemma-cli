# Fixture

## Architecture

Handlers call services; services call repositories. Only repositories touch the database.

## Commands

- `make build` compiles the service.
- `make test` runs the unit tests.

## API request validation

> Scope note: this guidance is meant for src/api/**/*.go, src/handlers/**/*.go.

Validate every request body at the boundary and return problem+json errors.

## Specialist guidance: reviewer

Reviews pull requests for API breakage

Review diffs for backwards-incompatible API changes.

Declared tools in the source definition: Read, Grep.

## Rules

- **SHOULD** Run `gofmt -w .` before committing.
