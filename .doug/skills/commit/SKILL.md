---
name: commit
description: Commit staged changes using conventional commits. Use when asked to commit, make a commit, or save changes.
---

# Commit

Commit staged changes using the [Conventional Commits](https://www.conventionalcommits.org/) format.

## Commit Message Format

```
<type>[optional scope]: <description>

[optional body]

[optional footer(s)]
```

### Types

| Type | When to use |
|------|-------------|
| `feat` | A new feature |
| `fix` | A bug fix |
| `refactor` | Code change that neither fixes a bug nor adds a feature |
| `docs` | Documentation only changes |
| `test` | Adding or updating tests |
| `chore` | Maintenance tasks (deps, CI, tooling, etc.) |
| `style` | Formatting, whitespace, semicolons — no logic change |
| `perf` | Performance improvement |
| `ci` | CI/CD configuration changes |
| `build` | Build system or dependency changes |
| `revert` | Reverting a previous commit |

### Scope

Optional. A noun in parentheses describing the section of the codebase affected.

Examples: `feat(parser):`, `fix(lexer):`, `refactor(eval):`

### Description

- Use imperative, present tense: "add" not "added" or "adds"
- Don't capitalize the first letter
- No period at the end

### Body

Optional. Use when the description alone isn't enough context. Explain **what** and **why**, not how.

### Breaking Changes

Add `!` after the type/scope and include a `BREAKING CHANGE:` footer:

```
refactor(api)!: rename Eval to Evaluate

BREAKING CHANGE: Eval has been renamed to Evaluate for clarity.
```

## Process

1. Review the diff to understand what changed — use `git` with `diff --cached` or `diff` as appropriate
2. Determine the appropriate **type** and optional **scope** from the changes
3. Write a concise **description** in imperative mood
4. Add a **body** only if the description is insufficient to understand the change
5. Call the `Commit` tool with the formatted message
