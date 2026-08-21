# Domain docs

How the engineering skills should consume this repo's domain documentation when exploring the codebase.

## Before exploring, read these

- **`CONTEXT.md`** at the repo root.
- **`docs/adr/`** for decisions that touch the area being changed.

If either does not exist, proceed silently. Do not flag its absence or suggest creating it up front. The `/domain-modeling` skill creates these files lazily when terms or decisions are resolved.

## File structure

This repo uses a single-context layout:

```text
/
├── CONTEXT.md
├── docs/adr/
└── src/
```

## Use the glossary's vocabulary

When `CONTEXT.md` and its glossary exist, use their defined terms whenever output names a domain concept—in an issue title, refactor proposal, hypothesis, or test name. Do not drift to synonyms the glossary explicitly avoids.

If the glossary exists but does not define the needed concept, reconsider whether the term belongs to the project or note the gap for `/domain-modeling`.

## Flag ADR conflicts

If output contradicts an existing ADR, surface the conflict explicitly rather than silently overriding it.
