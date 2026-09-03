# Exclusions

## Overview

Acme Ledger is a double-entry accounting service.

## Architecture decisions

- Use an append-only ledger table
  - Never write an UPDATE or DELETE against the entries table.
