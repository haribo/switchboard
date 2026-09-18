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
| `hourglass_top` | waiting on another session | a dependency it declared, still standing |
| `autorenew` | working | anything else while the session is active |
| `pause_circle` | idle | the session declared itself idle |

The first three are **worked out by the service**, not declared. A session
already says what it published; asking it to also keep a status in sync would add
a field that can go stale. Derivation cannot go stale.

Waiting on the PO is not the only way to be stopped. A session paused on another
session's work names it — the session, and the issue it is paused on — and the
table derives the state from whether that pause still stands:

```
acme-dev4   ⧗   #1826   waiting on acme-dev2 #1889 — step 2 cannot start
```

**It lifts itself.** The moment `acme-dev2` stops declaring #1889 — it moved on,
or retired — the row is working again, with nothing to clear by hand, exactly as
*waiting on you* lifts when the PO answers.

A session cannot declare itself stopped with nothing to point at: `waiting_on`
and `waiting_for` go together, and one without the other is refused. A freely
declared "I am blocked" would be the same lie in the other direction — forgotten
once, it holds all day.

The mark is grey, not amber. Amber on this page means *you*; a session paused on
a peer needs nothing from the PO.

**An issue shows as `#1886`, and links to the address it came from.** The number
is the URL's last segment, and only when that segment really is a number: an
address that is not an issue URL is shown as it stands, with no `#`, because a
reference that was not understood must look like an address rather than like a
reference to issue "milestones". One definition serves both renderings — the page
and the manager's board diverged once, the board printing `#https://…` while the
page already showed `#1886`.

**Issues link because the session sends the whole address.** It knows the
repository it works in; the service does not, holds none, and will not be given
one — naming another repository is what this repository must not do, and a bare
number resolved against a configured repository links to somebody else's issue.
A bare number is refused on the way in. Rows written before that rule keep
theirs, and show as plain text: a link that leads nowhere is worse than no link.

**The last column carries two readings.** *How long* the session has been in this
status — which does not move on a repeated declaration, so the table can say
"40 min" rather than "just now" five times a minute — and, when it applies,
*whether anything is still arriving*:

```
acme-dev1   working   #142   action bar, label     41 min
acme-dev2   working   #145   sign-in screen        41 min · quiet 1h04
```

Both rows entered their status at the same moment. Only the second has said
nothing since. Without the second reading they render identically, and a session
that died an hour ago looks exactly like one working steadily — the state the PO
would least think to investigate.

**The mark appears only past a threshold**, 30 minutes by default
(`--quiet-after`). A number beside every row on a healthy fleet is noise, and
this page's discipline is that only what needs attention calls out. The threshold
is the service's: it sends `quiet` already decided, so the page never guesses.

It says **quiet**, never *dead* or *stale*. What the service observes is that
nothing arrived; it cannot know whether the session is on one long task or gone,
and a page must not display a distinction it has no means of observing. The
hover text says as much.

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

**When the wording is the obstacle**, *Explain simply* records that the PO cannot
act on the ask as written. It sits below the answers and is quieter than them:
asking for a rewording is not answering, and a control of equal weight next to
the options invites a click meant for one of them.

The ask stays on the page — it is still waiting for an answer, just not in those
terms — and says the request was made, so the PO does not click again wondering
whether the first one landed. The plain-words version arrives **beside** the
original, never in its place: the PO may want the first wording back, and a later
reader needs to see what was actually asked. It can be asked again if the
rewording did not help.

This is the one case where a card is rebuilt. New content arrived, and a card
that never changed would leave the PO staring at the wording they already said
they could not use — so the rule bends here, and only here, and still never while
somebody is typing into it.

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
