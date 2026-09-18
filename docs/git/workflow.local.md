# Workflow — switchboard

Additions to [workflow.md](workflow.md) and
[workflow.ext-develop-main.md](workflow.ext-develop-main.md), proper to this
repository.

## Releasing

`develop` reaches `main` through a merge commit, and its subject is **chosen**,
never left to GitHub:

```bash
gh pr create --base main --head develop --title "chore(release): vX.Y.Z"
gh pr merge <n> --merge --subject "chore(release): vX.Y.Z (#<n>)" --body ""
git switch main && git pull && git tag -a vX.Y.Z -m "vX.Y.Z" && git push --tags
```

Left to itself, GitHub writes `Merge pull request #N from haribo/develop` — the
one non-conventional subject in an otherwise clean history, and the one that
cannot be corrected afterwards: the merge lands on a permanent branch, gets
tagged, and release artefacts point at that tag.

The tag is what the deployed build reports. Until the first one, `just health`
answers a commit hash; from then on it answers a version.

## Hooks

`just hooks` points git at `.githooks`, where the pre-commit guard lives. Run it
once after cloning — the file is inert otherwise.
