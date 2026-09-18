# Issue conventions

See also: [commits.md](commits.md) for commit conventions, [workflow.md](workflow.md)
for branching and PR rules.

## Title

```
<imperative description>
```

A pure description — the type is carried by labels, not by the title.

## Rules

1. Imperative present tense ("add" not "added")
2. Lowercase, no period
3. Max 72 characters
4. Descriptive and concise — stands alone without extra context

## Labels

Prefixed labels categorize issues; the title must not duplicate label information.

### Type (required — pick one)

| Label | Usage |
|---|---|
| `type: bug` | defect or malfunction |
| `type: feature` | new feature or improvement |
| `type: chore` | CI, tooling, maintenance, cleanup |
| `type: docs` | documentation change |

### Priority (optional)

| Label | Usage |
|---|---|
| `priority: critical` | requires immediate attention |

### State (optional — closing qualifiers)

| Label | Usage |
|---|---|
| `state: wontfix` | will not be worked on |
| `state: duplicate` | already exists |
| `state: invalid` | not a valid issue |

## Self-contained content

An issue that may be implemented without the originating discussion — another
session, a different developer, your future self — must let the implementer
execute without asking questions:

- **Why** the change is needed (1-3 lines)
- **Decisions already taken** (no "TBD" unless truly open)
- **Explicit mappings** for refactors (current → target, per artifact)
- **Validation criteria** (grep patterns, file lists, test names)
- **PR strategy** when the issue spans several tasks: one PR or several, and why
- **Out of scope**, to prevent scope creep

Trivial issues (typos, one-line config) are exempt. The test: if the implementer
would need to ask "what did you mean by X?", the issue is incomplete.

## Approval before creation

An issue is never filed straight from a request. Title, labels and body are
presented first, and the author validates them — the body is what a later
session executes without the conversation that produced it, so it is reviewed
while that context is still there. Trivial issues are exempt, on the same
boundary as above.

## Anchoring decisions

A decision taken in conversation — especially one where an alternative was
rejected — becomes an issue targeting the document that should carry it: the
design docs for product behaviour, an ADR for a structural decision. Trigger
when all three hold: the decision is not derivable from the code, an alternative
was explicitly rejected, and it will influence later decisions.

## Epics

When a change splits into ~3 or more related pieces of the same subsystem, write
an **epic** plus short sub-issues rather than independent issues:

- **The epic** carries the shared context once — why, locked decisions,
  out of scope — and lists the sub-issues as a checklist.
- **Sub-issues stay short**: `Part of #<epic>`, a Build section, a Validation
  section. No repetition of the epic's context.
- **Don't over-epic**: an isolated change stays a plain issue. The threshold is
  coherence of design, not size.

## Additions

- Extensions deployed with this file: `issues.ext-*.md` in this directory
- Additions proper to this repository: `issues.local.md`
