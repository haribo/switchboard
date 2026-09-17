# The PO's page

One page, kept open all day. Everything below follows from one sentence in the
brief: *a text scrolling while the PO reads is a blocking defect.*

The routes behind it are in
[the OpenAPI document](../../internal/api/openapi.yaml); this file is about what
the page shows and why.

## Sessions, as a table

Five columns: dev, state, issue, what's happening, for how long.

**The state is an icon**, with a legend under the table — Material Symbols,
inlined in the binary rather than loaded from a font host. The service runs on a
machine that may have no network, and an icon that silently falls back to its own
name is worse than no icon.

| icon | state | how it is known |
|---|---|---|
| `block` | blocked | an open `blocked` event from that session |
| `back_hand` | waiting on you | an open ask sitting with the PO |
| `autorenew` | working | anything else while the session is active |
| `pause_circle` | idle | the session declared itself idle |

The first two are **worked out by the service**, not declared. A session already
says what it published; asking it to also keep a status in sync would add a field
that can go stale. Derivation cannot go stale.

**Issues link when the address can be resolved.** A session that sends the full
address of its issue gets a link with no configuration at all — it knows the
repository it works in, the service does not, and does not guess. A bare number
resolves only against a configured `--repo`, and shows as plain text otherwise:
a link that leads nowhere is worse than no link.

**"What's happening" stays on one line**, clipped with an ellipsis, with the full
text in the title attribute. Every row keeps the same height, so the table stays
scannable however long a session's note runs.

**A row can leave.** A session is retired with `DELETE /v1/sessions/{name}`,
which is what keeps the table short: a typo (`--session dev33`) would otherwise
hold a line for the life of the database, and a dev stopped for a few days would
sit there as `idle` until the PO learned to skip lines.

What it published stays. An ask carries its author's name and the card renders
from that, not from a lookup in the table — so a card outlives the row that
raised it, and the page shows it unchanged. A session with open asks is refused
retirement: answering or withdrawing them first is what stops the PO holding a
card whose author no longer exists.

**The order is always by name.** Every dev keeps the same line all day, so the PO
learns where to look instead of reading. Sorting by urgency was considered and
turned down: it puts what matters on top, but it makes rows jump every time a
session changes state — movement, in the one place that must not move.

## Your call

What is addressed to the PO, and nothing else. Each ask carries:

- **who is asking** — a role tag, the session name, the issue. The role earns the
  tag because an ask from the manager is not decided like an ask from a dev. When
  the name would merely repeat the role, only the tag shows.
- **a title**, one line, what the PO skims to decide whether to take this now;
- **a description**, sanitized HTML, what they read once they have;
- **the offered options**, as buttons;
- **a free text field**, for everything the options did not foresee.

A `validation` also carries its link, opened in another tab.

## Nothing moves

- A card is **built once and never redrawn**. Whatever the PO typed stays, for as
  long as they leave it there.
- The order is the server's, oldest first, and the DOM is only reordered when
  **nobody is typing** — moving a focused field would drop the caret.
- If an ask leaves the list while the PO is typing into it — answered by somebody
  else, or **withdrawn** by its author — the card is not pulled out from under
  them. It is disabled in place, says *This is no longer waiting on you*, keeps
  what was typed, and waits for the PO to close it.

  The wording is deliberately silent on *why*. The page only knows the ask is no
  longer in `for_you`; it does not know whether it was answered or withdrawn.
  Naming a cause it cannot establish would be the page asserting something it has
  not observed — the same failure as a link that leads nowhere. Fetching the
  reason would mean a second call for a case that costs the PO one click, so the
  page states what is certain and stops there.
- `info` never reaches this list. It sits in a folded section at the bottom,
  closed, with a count.
- If the service stops answering, the page says **service unreachable**. An empty
  page and a dead one must not look alike.

## The tab is the only thing that calls out

The title carries the count — "(3) switchboard" — and the favicon turns amber.
No notification bubble, no sound, nothing that takes the cursor. The count drops
the moment the PO answers, not on the next refresh.

## Taking an answer back

Ten seconds. The card holds its place and folds into a strip with the answer
given, an "Undo" button and a countdown, then clears. Nothing above it moves
while it is there. The server owns the window and sends it to the page, so a
reload does not cost the chance to undo, and a page left open cannot offer an
undo the server will refuse. See
[ADR-0003](../adr/0003-ten-second-undo.md).
