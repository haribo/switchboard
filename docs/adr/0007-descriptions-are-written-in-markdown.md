# ADR-0007: a description is written in Markdown

## Status

Accepted. Supersedes the input format of [ADR-0004](0004-descriptions-take-a-whitelist.md);
the whitelist it decided is unchanged.

## Context

ADR-0004 settled what a description may contain — a short list of tags — and, by
saying so, also settled how it had to be written: as HTML. That part never
survived contact with the sessions.

The first live database says it plainly. Seven of eight descriptions carried no
markup at all: blank lines between paragraphs, `**` around what mattered,
backticks around a file name. HTML swallowed the blank lines, so ten lines
arrived as one block, and the asterisks and backticks showed as themselves. The
body that was hardest to read was the longest one — the one that most needed the
paragraphs it had been given.

This is not carelessness. A session writes Markdown in issues, in commit
messages, in its own answers, everywhere it types. Asking it to write HTML here
and Markdown everywhere else is asking it to remember which window it is in.

## Decision

A description is **parsed as Markdown, then sanitized** — in that order.

The sanitizer stays exactly as ADR-0004 left it, and stays **last**: it is the
only thing between a description and the PO's page, so it must see the final
tags, including the ones the renderer produced. A test inverts the order and
fails, so the ordering cannot be lost to a tidy-up.

Two details follow from how sessions actually write:

- **a single newline is a line break.** A session laying out three lines means
  three lines; Markdown's default would join them.
- **raw HTML in the source is rendered, not dropped.** A session that writes
  `<a href="…">` — as ADR-0004 asked it to — still gets its link, because the
  sanitizer is what decides, not the renderer. Descriptions written before this
  change read the same as they did.

`pre` joins the whitelist: a fenced block renders as `pre` + `code`, and a
session pasting a command or a log line is the ordinary case here. Nothing else
is added — an image or a heading in an ask is still text.

## Consequences

The service depends on `goldmark`. It is the renderer Go projects converge on,
it is pure Go, and it has no configuration surface wide enough to reopen the
safety question — which the sanitizer answers anyway.

Markdown that the whitelist has no room for degrades to its own source text: a
heading shows as `## like this`. That is the same rule as before — nothing is
silently emptied — applied one layer earlier.

**The manager's board keeps printing titles alone.** A body is written for the
PO's page; a 1700-character one scrolling through the manager's terminal is the
defect this tool was built to remove. `switchboard board --json` carries the body
for anything that needs it.
