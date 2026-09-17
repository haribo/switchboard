# Claude Guidelines

AI directives only. Project conventions live in `docs/`.
One rule per line where possible.

## General

- The project uses `just` (justfile), not `make`
- Everything is written in English: code, comments, documentation, CLI and page
- Lowercase file names
- Check the existing docs before creating a file
- This repository is not the product: it holds no domain logic, and names no
  other repository
- Anchor a non-obvious decision — above all one where another route was ruled out
  — in `docs/design/` or an ADR before building on it
- The API contract is `internal/api/openapi.yaml`, and it is the only description
  of the routes: a route is added to the routing table and to the spec in the
  same change, or the suite fails. Never describe the API a second time in prose
- ADR lifecycle: never delete one; a reversal is a **new** ADR, and both carry the link

## What this tool does not do

- It duplicates nothing from GitHub: issues, labels, pull requests and comments stay there
- It drives no session: it carries states and asks, nothing else
- It does not read Claude Code's private registry (`~/.claude/sessions/*.json`,
  `/run/user/*/cc-socks/`) — see `docs/adr/0001-waking-by-long-poll.md`
- It never puts an event's content in a signal: a count is the whole message
- A description is never stored or rendered unsanitized — `richtext.Clean` on the
  way in, see `docs/adr/0004-descriptions-take-a-whitelist.md`

## The PO's page

- Nothing scrolls, nothing reorders under the reader, nothing animates
- A card is built once and never redrawn; a field being typed into is never touched
- An empty page and an unreachable service must never look alike
- The browser tab is the only thing allowed to call out

## Tests

- A fix starts with a failing test that reproduces the defect
- A passing test can measure nothing: prove red before trusting green
- Never modify an existing test without explicit approval
- No `sleep` in the batching tests: the clock is injected

## Git

- `develop` is where work is committed; `main` carries the stable versions
- Conventional commits, one line, no AI references
- Never commit or push without explicit approval
- A deployed build is stamped with `git describe --dirty`, so what runs is always
  traceable to a commit — or visibly not one
