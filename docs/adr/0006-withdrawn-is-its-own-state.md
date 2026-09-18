# ADR-0006: a withdrawn ask has its own state, not `done`

## Status

Accepted.

## Context

An ask sometimes answers itself. A dev is told to check the board before
escalating, so it occasionally finds the answer just after publishing the
question; the manager hits the same case when it reads an issue and sees the
decision was already written. Until now the only way for such an ask to leave
the PO's page was to be answered — so the PO was made to decide something that
no longer meant anything, and the tab counted it.

The state machine already had a state for an event that closes without an
answer: `done`, used by `ack` for the `info` nobody has to act on.

## Decision

A withdrawn ask gets **its own state**, `withdrawn`, and records `closed_by`.

`done` means *it only had to be read*. Reusing it for a withdrawal would merge
two facts a reader needs apart: an `info` that was noted, and a question that was
retracted. The board would show both as closed with nothing to tell them apart,
and the first person to ask "what happened to that question?" would have no
answer in the data.

Who withdrew it is stored because somebody is accountable for a card vanishing
from under the reader. The author may withdraw their own ask; the manager may
withdraw any of them, because dispatching is their job. Nobody else.

An **answered** event refuses withdrawal. Taking an answer back is
[ADR-0003](0003-ten-second-undo.md)'s undo, bounded to ten seconds and reopening
the event. The two do opposite things — one closes an ask that needs no answer,
the other reopens one whose answer was a mistake — and letting them overlap would
make "the card is gone" ambiguous.

## Consequences

`await` had to gain a second way to stop waiting. It blocked until a reply
existed; a withdrawal would have left it waiting out its timeout, and a caller
that eventually gave up could not tell a withdrawal from silence. It now returns
on any state that is no longer `open`, and the CLI exits with a distinct code so
a session can branch without parsing prose.

The schema grew a column, which is migration 2 — the first real exercise of
[ADR-0005](0005-versioned-schema.md)'s machinery on a populated database.

The PO's page cannot say *why* a card it was holding has left the list: it only
knows the ask is no longer addressed to them. It states that, and does not guess
at a cause it has not observed — see `docs/design/po-page.md`.
