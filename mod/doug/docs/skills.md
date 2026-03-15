> Doug can create skills. Ask it to build one for your use case.

# Skills

Skills are self-contained instruction packages that Doug loads on-demand. A skill provides specialized workflows, setup instructions, helper scripts, and reference documentation for specific tasks.

Doug implements the [Agent Skills standard](https://agentskills.io/specification).

## Locations

Doug scans the workspace for skills in:

- `.doug/skills/` — Doug-specific skills (may reference Doug tools like ReadFile, Research, Rabbithole)
- `.agents/skills/` — Cross-harness skills (per the Agent Skills standard)

Discovery rules:
- Direct `.md` files in the skills directory root
- Recursive `SKILL.md` files under subdirectories

## How Skills Work

1. At startup, Doug scans skill locations and extracts names and descriptions from YAML frontmatter
2. The system prompt includes available skills in `<available_skills>` XML format
3. When a task matches, use ReadFile to load the full SKILL.md
4. Follow the instructions, using paths relative to the skill file's directory

This is progressive disclosure: only descriptions are in context, full instructions load on-demand.

## Skill Structure

A skill is a directory with a `SKILL.md` file. Everything else is freeform.

```
my-skill/
├── SKILL.md              # Required: frontmatter + instructions
├── scripts/              # Helper scripts
│   └── process.sh
├── references/           # Detailed docs loaded on-demand
│   └── api-reference.md
└── assets/
    └── template.json
```

### SKILL.md Format

```markdown
---
name: my-skill
description: What this skill does and when to use it. Be specific.
---

# My Skill

Instructions for the agent go here.
```

## Frontmatter

| Field | Required | Description |
|-------|----------|-------------|
| `name` | Yes | Lowercase a-z, 0-9, hyphens. Must match parent directory. |
| `description` | Yes | What the skill does and when to use it. |
| `disable-model-invocation` | No | When `true`, skill is hidden from system prompt. |

### Name Rules

- Lowercase letters, numbers, hyphens only
- No leading/trailing hyphens
- No consecutive hyphens
- Must match parent directory name

### Description Best Practices

The description determines when Doug loads the skill. Be specific.

Good:
```yaml
description: Extracts text and tables from PDF files, fills PDF forms, and merges multiple PDFs. Use when working with PDF documents.
```

Poor:
```yaml
description: Helps with PDFs.
```

## Example

```
.doug/skills/
└── code-review/
    ├── SKILL.md
    └── checklist.md
```

**SKILL.md:**
```markdown
---
name: code-review
description: Structured code review with a checklist. Use when asked to review code changes or PRs.
---

# Code Review

## Process

1. Read the changed files with ReadFile
2. Check each item in [the checklist](checklist.md)
3. Report findings grouped by severity
```

## Doug-Specific Considerations

Skills in `.doug/skills/` can reference Doug-specific tools:
- `ReadFile` — read file contents
- `EditFile` — surgical edits
- `Write` — create/overwrite files
- `Glob` — find files by pattern
- `Grep` — search file contents
- `Research` — launch read-only sub-agent
- `Rabbithole` — launch read-write sub-agent
- `Commit` — commit staged changes

Skills in `.agents/skills/` should use generic language to work across harnesses.
