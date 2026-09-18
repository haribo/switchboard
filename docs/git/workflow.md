# Git workflow

See also: [commits.md](commits.md) for commit conventions, [issues.md](issues.md)
for issue conventions.

The branch model itself is not in this file — it differs between repositories.
See the extension deployed here, or `workflow.local.md`.

## Permanent branches

A permanent branch is never pushed to directly: every change reaches it through
a pull request. Which branches are permanent, and what each one deploys, is
stated by the branch-model file.

## Issue-first workflow

Every change starts with an issue, except trivial ones (typo, formatting,
dependency bump) where the PR alone suffices.

- The issue describes the **what** and the **why** — the PR describes the **how**
- The branch name carries the issue number, for traceability
- The PR body references the issue with `Closes #N`, so merging closes it

```
issue #12 → branch feat/12-short-description → PR "Closes #12" → merge
```

## Branch naming

```
feat/12-short-description
fix/34-short-description
refactor/56-short-description
docs/78-short-description
chore/short-description
```

The prefix matches the commit type. The issue number follows the slash.
Kebab-case. The number may be omitted for a trivial `chore` or `style` with no
issue.

## CI gating

A check required to merge must be **scheduled on every pull request**, even when
it has nothing to do. A workflow-level `paths:` filter leaves its checks
unscheduled — and therefore pending forever — on a pull request that does not
touch those paths. Filter inside the workflow with a path-detection job instead:
a skipped job reports success, an unscheduled one reports nothing.

## Rules

- Never push directly to a permanent branch — always through a PR
- One logical change per PR — unrelated work goes to its own PR
- Feature branches stay short-lived: days, not weeks
- Rebase on the target branch before opening the PR
- A PR introducing user-facing behaviour updates the design docs in the same
  diff; bug fixes and refactors are exempt

## Additions

- Extensions deployed with this file: `workflow.ext-*.md` in this directory
- Additions proper to this repository: `workflow.local.md`
