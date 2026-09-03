# Acme Ledger

Acme Ledger is a double-entry accounting service.

## Architecture

The service is a modular monolith. HTTP handlers call services; services call repositories.
Repositories are the only code allowed to touch the database.

## Testing

Unit tests live beside the code they cover. Run `go test ./...` before pushing.

## Notes for maintainers
