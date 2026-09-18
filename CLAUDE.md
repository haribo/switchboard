# Claude Guidelines

AI directives only. Project conventions live in `docs/`.
One rule per line where possible.

- This file takes precedence over auto-memory. If an auto-memory entry contradicts a rule here, follow this file and update or remove the conflicting memory; do not act on the stale memory.

## General

- The project uses `just` (justfile), not `make`
- Everything is written in English: code, comments, documentation, CLI and page
- Lowercase file names
- Check the existing docs before creating a file
- This repository is not the product: it holds no domain logic
- Never carry real-world context from outside this repository into what it publishes: another project's name or details, or the author's identity, handle or accounts. This repository may name itself; it may not name what sits next to it. Git metadata — remote URL, commit authorship — is out of scope; the rule covers written content: docs, code, comments, examples, commit messages, issues
- When an example needs a project, invent one, with an obviously fictitious and banal name (`acme`, `example-app`). Do not reproduce the identifying structure either — directory layout, service names, domain vocabulary. A placeholder that resembles the original protects nothing
- Before touching anything on a non-trivial task, explain the problem simply and concisely — with an example when an example is what makes it clear. What is validated is that explanation, not the issue or the request that prompted it. Build after
- Exempt: trivial changes (typo, formatting, dependency bump). Friction that buys nothing is how a rule gets routed around
- Never assume a produced artifact matches its request — a generation, a render or a transform routinely ignores or distorts instructions. Before judging, presenting or consuming it, inspect what was actually produced and state the invariant it must satisfy; where the invariant is checkable, write the check that counts the violations. Claiming "it now matches X" without having verified is a defect.

## Collaboration

- When the user asks for an opinion, be severe, honest and challenging — the goal is code that meets professional standards, not the user's agreement. Zero flattery, no hedging, no false balance
- Verdict first (1 line), then 3 bullets of substance at most. Say plainly when something is wrong, and say so when it is right — an unearned validation is a defect
- Quality over satisfaction — push back on over-engineering, incoherence, and unjustified additions, including when user-proposed
- Critique constructively: acknowledge what is sound first, cite established standards (RFC, WCAG, NN/G, language idioms) rather than personal preference, propose the correction — never mere opposition, never a strawman of the user's position
- The user decides in the end: challenge until the decision, then execute it in full. If a debate cycles past 3 iterations on the same axis without converging, propose to decide rather than continue

## Documentation

- ADR lifecycle: never delete an ADR; a reversal is a **new** ADR, and both sides carry the link — `Superseded by ADR-NNN` on the old, `Supersedes ADR-MMM` on the new. A one-sided link is how the chain rots
- An ADR whose decision no longer applies, with no replacement, is marked `Deprecated` — never edited away or moved
- In-place edits only for corrections of form and for clarifications that do not change the decision
- Code is never source of truth — a code/design disagreement means the code is the bug, or the design needs an explicit amendment, never both silently
- If the design is silent on a needed behavior, write the design first, then the code
- Anchor a confirmed non-obvious decision — especially one where an alternative was rejected — in the design docs or an ADR before building on it
- The API contract is `internal/api/openapi.yaml`, and it is the only description of the routes: a route is added to the routing table and to the spec in the same change, or the suite fails. Never describe the API a second time in prose

## What this tool does not do

- It duplicates nothing from GitHub: issues, labels, pull requests and comments stay there
- It drives no session: it carries states and asks, nothing else
- It does not read Claude Code's private registry (`~/.claude/sessions/*.json`,
  `/run/user/*/cc-socks/`) — see `docs/adr/0001-waking-by-long-poll.md`
- It never puts an event's content in a signal: a count is the whole message
- It holds no repository address of its own, and takes none as a setting. An
  issue arrives as its full URL or not at all — resolving a bare number would
  mean naming another repository, and would link to the wrong issue the moment a
  session works somewhere else
- A description is never stored or rendered unsanitized — `richtext.Clean` on the
  way in, see `docs/adr/0004-descriptions-take-a-whitelist.md`

## Input rules

- Validation belongs on the way in. A read or a delete accepts anything the store
  can hold: applying a creation rule to them rejects exactly the data that most
  needs reaching — a row written before the rule existed
- Tightening an accepted shape is not finished until the routes that reach
  existing rows have been checked against the new rule

## The PO's page

- Nothing scrolls, nothing reorders under the reader, nothing animates
- A card is built once and never redrawn; a field being typed into is never touched
- An empty page and an unreachable service must never look alike
- The browser tab is the only thing allowed to call out

## Working an issue

- No issue is trusted — not one written a year ago, not one written an hour ago, not one you wrote yourself. Age is not the criterion: an issue can describe code written the same day and still have a false central claim
- Verify every claim against the current code and docs, and cite `file:line` for each one confirmed — "I read the code" is not verification, a citation the reader can re-open is. Say explicitly what you could not confirm
- Record the outcome in the issue itself, never only in the conversation: a claim that proved false becomes a correction comment, an issue already delivered is closed with the evidence, an issue whose premise drifted gets an audit comment and a re-scope. The issue is what a later session reads; the conversation is not
- Then, before touching anything: explain what the issue actually consists of — simply and concisely, with an example when an example is what makes it clear — including what the verification changed about it
- Wait for explicit validation of that explanation. No implementation without it. What is validated is the problem as explained, and the approach too when more than one credible approach exists — agreeing on the problem is not agreeing on the fix
- Implement the validated scope and nothing else: an unrelated bug, or a good idea found on the way, becomes its own issue and never an extra commit on this branch
- Exempt: trivial changes (typo, formatting, broken link, dependency bump) — the same boundary that exempts them from needing an issue. Friction that buys nothing is how a rule gets routed around

## Testing

- Bug fixes start with a failing test that reproduces the bug: write the test first and watch it fail, then fix, then re-run it green (red → green)
- That test stays as the regression test for this bug — reference the issue number in it, so a later reader knows what it guards and does not delete it as noise
- Bug fixes must reproduce the failure from observed evidence (logs, network capture, repro steps); never invent the failure scenario from a hypothesis
- Never modify existing tests without explicit approval
- If a test fails after code changes, report it instead of fixing it silently
- Adding new tests is always allowed
- When you add or modify user-observable code, propose the corresponding test in the same response as the code change — a gate at push or review time is a backstop, not the discipline
- A passing test can measure nothing: prove red before trusting green
- No `sleep` in the batching tests: the clock is injected

## Git

- `develop` is where work is committed; `main` carries the stable versions
- Commit conventions: follow `docs/git/commits.md` strictly
- Git workflow: follow `docs/git/workflow.md` strictly
- Issue conventions: follow `docs/git/issues.md` strictly
- Conventional commits, one line, no AI references
- Never commit or push without explicit approval
- A deployed build is stamped with `git describe --dirty`, so what runs is always
  traceable to a commit — or visibly not one
