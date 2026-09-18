# Branch model — `develop` and `main`

Extension to [workflow.md](workflow.md), deployed to the repositories using two
permanent branches.

## Branches

| Branch | Role |
|---|---|
| `main` | stable versions |
| `develop` | where work lands |

Both are permanent — never pushed to directly, always through a PR.

## Feature workflow

1. Branch from `develop`, with the issue number in the name
2. Work, commit, push
3. Rebase on `develop` before opening the PR
4. Open the PR **targeting `develop`**, referencing the issue with `Closes #N`
5. Wait for CI to pass
6. Squash merge

A feature PR never targets `main`.

## Release workflow

`develop` reaches `main` through a merge commit, never a squash: squashing a
release would collapse the history that `main` exists to carry.

## Merge strategy

| Target | Strategy |
|---|---|
| feature → `develop` | **squash** |
| `develop` → `main` | **merge commit**, with an explicit conventional subject |

Never merge a feature PR with `--merge`. Never target `main` with a feature PR.
