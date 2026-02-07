---
name: committing
description: How to write commit messages for this project. Use when committing changes.
---

# Committing

This project uses [Conventional Commits](https://www.conventionalcommits.org/).

## Format

```
type(scope): short summary

Optional longer description as a paragraph wrapping at ~72 characters.
Explain what changed and why, not how.
```

## Types

- `feat` — new feature
- `fix` — bug fix
- `chore` — maintenance (deps, golden files, etc.)
- `style` — formatting, no logic change
- `test` — adding or updating tests
- `refactor` — restructuring without behavior change

## Scope

The scope is optional but preferred when the change is localized. Use
the package or subsystem name, e.g. `fmt`, `lsp`, `parser`.

## Rules

- Subject line: imperative mood, lowercase, no period, ~50 chars
- Body: wrap at ~72 chars, separated from subject by a blank line
- Body should be a concise paragraph, not bullet points
- If the change is simple enough, the subject line alone is fine
- Routine fixes (lint, typos, formatting) typically need no body unless
  something particularly interesting or involved was done
