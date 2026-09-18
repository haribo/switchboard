# Git conventions

## Branches

`develop` is where work is committed. `main` carries the stable versions and is
reached by merging, never by committing onto it — a pre-commit hook refuses that,
and `just hooks` is what makes the hook run (see
[operations.md](design/operations.md)).

## Commit messages

Conventional commits, **one line**, no body:

```
feat: let a session be retired
fix: render an issue reference as its number
docs: adopt the shared ruleset rules in CLAUDE.md
chore: scan secrets and vulnerabilities in ci
```

No AI references — no `Co-Authored-By`, no `Generated with`.

## One issue, one commit

Commit the moment an issue is green, **before starting the next one**. Not at the
end of a batch: by then the boundary between two pieces of work is already lost,
and the message gets written for whichever one is still in mind.

This is not bookkeeping. A commit that carries two issues cannot be reverted
without taking both, and the history stops answering "what did this change do?".

It has gone wrong once, and mechanically: #15 and #16 landed in
`52ca6cd` under a message naming only #15, because the request had been for
"new issues" in the plural and the commit came at the end of the batch.

## Read what is staged

`git status` before every commit. `git add -A` stages whatever is lying around —
a scratch file, the next issue's work — and the message is then written from the
work remembered rather than the work staged. `git diff --cached --name-only`
answers the question directly: does every one of these files belong to the issue
named in the message?

## What a deployed build carries

Builds are stamped with `git describe --tags --always --dirty`, so `just health`
always says which commit is answering — and says `-dirty` when it is not one. A
deploy from a modified tree is allowed and says so; it is never mistaken for a
commit.
