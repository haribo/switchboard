# Commit conventions

See also: [workflow.md](workflow.md) for branching and PR rules.

## Format

```
<type>(<scope>): <description>
<type>(<scope>)!: <description>   ← breaking change
```

## Types

`feat` | `fix` | `docs` | `style` | `refactor` | `perf` | `test` | `chore` | `ci` | `build`

## Scope

Recommended on all commits. It names the area of change — a domain, a layer, a
package or a tool. May be omitted for a generic `style` or `chore` spanning the
whole project.

The vocabulary of scopes is proper to each repository: see `commits.local.md`.

## Breaking changes

Append `!` after the scope:

```
feat(auth)!: remove the legacy login flow
```

## Squash merge commits

When a feature PR is squash-merged, GitHub appends the PR number:

```
type(scope): description (#PR)
```

The PR title must follow `type(scope): description` — without `(#PR)`, GitHub
adds it on merge.

## Rules

1. Single line only — no body, no footer
2. Max 72 characters (excluding the auto-appended `(#PR)` suffix)
3. Imperative present tense ("add" not "added")
4. No capital letter, no period
5. No AI references or promotional content

## Additions

- Extensions deployed with this file: `commits.ext-*.md` in this directory
- Additions proper to this repository: `commits.local.md`
