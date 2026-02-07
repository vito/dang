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

## Granularity

Each commit should contain exactly one logical change. If a task
involves multiple distinct changes (e.g. a refactor, a new feature,
a formatter fix, and a new skill file), split them into separate
commits. Each commit should only stage its relevant files — avoid
`git add .` or staging unrelated changes. A good test: could you
write a clear, single-purpose subject line? If not, split it up.

## Staging

Stage specific files with `git add <file>...`. Do **not** use
`git add -p` — it requires interactive input and will hang.

If you need to commit only part of a file's changes, write the
file so it contains only the changes for the current commit, commit
it, then make the remaining changes afterward.
