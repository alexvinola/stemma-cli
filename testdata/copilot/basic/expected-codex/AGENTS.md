# Fixture

## Acme Ledger

Acme Ledger is a double-entry accounting service.

## API layer conventions

> Scope note: this guidance is meant for src/api/**, src/handlers/**.

- Validate every request body at the boundary.
- Return `application/problem+json` for errors.

## Architecture

The service is a modular monolith. HTTP handlers call services; services call repositories.
Repositories are the only code allowed to touch the database.

## Testing

Unit tests live beside the code they cover. Run `go test ./...` before pushing.

## Specialist guidance: reviewer

Reviews pull requests for API breakage

Review diffs for backwards-incompatible API changes and flag them explicitly.

Declared tools in the source definition: read, grep.
