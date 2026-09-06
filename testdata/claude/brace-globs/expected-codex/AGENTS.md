# Fixture

## Acme Frontend

A TypeScript frontend with a small Go service layer.

## Conventions

Prefer composition over inheritance.

## Asset conventions

> Scope note: this guidance is meant for app/**/*.css, app/**/*.scss, packages/**/*.css, packages/**/*.scss.

Design tokens come from the theme package; never hard-code a hex colour.

## Strict typing

> Scope note: this guidance is meant for src/**/*.ts, src/**/*.tsx, lib/**/*.go.

Use explicit types at module boundaries.
