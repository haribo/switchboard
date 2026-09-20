# ADR-0008: an observation sits beside the declaration, and never replaces it

## Status

Accepted.

## Context

A session says where it is — `active` or `idle`, with an issue and a line of
detail. Every state on the PO's table is built from that declaration and from
the open events, and both have the same blind spot: **a session cannot say that
it has stopped.** The last thing a stopped session declared was `active`, and
that is what the row keeps saying.

The service can derive silence — nothing has arrived for 30 minutes — and it
does. That derivation was on the PO's page and was taken off it by #43, for two
reasons written down in `docs/design/po-page.md`: it is the manager's business,
and for a session that declared once and stopped, `1h27` beside `quiet 1h26` is
one instant written twice. Both were right about the thing being removed.

Neither closes the hole. On 2026-09-20 the manager's own row read `working` with
a declaration fourteen hours old, and the page said nothing about it, because
silence is all the service has and silence is not evidence. A session paused on a
long foreground command is silent and working; a session that ended its turn is
silent and stopped. The service cannot tell them apart and must not pretend to.

**Someone else can.** The manager's host reports `busy` / `idle` / `shell` per
session, authoritatively. That observation exists, one caller away from the
board, and there is no way to put it there — it stays in that caller's terminal,
and the PO learns it by being told. Twice, it was the PO who noticed first.

Reading it ourselves is not an option: ADR-0001 rules out Claude Code's private
registry, for reasons that have not changed.

## Decision

A caller may **report what it observed of another session**. The observation is
stored beside that session's declaration and is never merged into it.

- **It is attributed and timestamped.** Who observed, what they observed, when.
- **It expires**, after 10 minutes by default (`--observation-valid-for`). An
  observation says something about *now*; past its validity the service stops
  emitting it at all, rather than letting a reader date it themselves.
- **It is not a sign of life.** Recording an observation leaves `updated_at` and
  `since_at` untouched: the observed session has not spoken, and an observation
  that reset the silence clock would erase the very thing it reports on.
- **A session cannot observe itself.** That is what declaring is for, and a
  second declaration channel is exactly what this must not become.
- **It changes no state.** Nothing is retired, nobody is woken, no row is marked
  stopped because someone said so. The disagreement is the finding; what to do
  about it belongs to the reader.

Only the two declared statuses can be observed, `active` and `idle`. The
observation must be comparable with the declaration, so it speaks the same words.

**The service keeps the latest observation per session**, not their history. One
observer exists today and a second reading of the same session says the same
thing; a history is a table, a migration and a display decision bought for a
question nobody has asked.

### Where it shows

**On the PO's page and on the manager's board**, which reverses the placement
half of #43. What #43 removed was a *derived* silence that restated the row's own
number. This is a different object: another party's reading, with its own source
and its own clock, saying something the row cannot say about itself. Reason 2 of
#43 does not apply to it. Reason 1 — "it is the manager's to act on" — is
answered by the evidence in #49: the manager did not act on it, and the PO
noticed first, twice.

**The mark appears only on a row that claims progress** — one reading `working`.
`blocked`, `waiting on you` and `waiting on another session` already say that
nothing is advancing; an observation of idleness there is true and pointless, and
this page's discipline is that only what needs attention calls out. An
observation that agrees with the declaration is not a finding either, and shows
nothing.

The service decides this, not its readers: it sends the observation already
weighed, the way it sends `quiet` already decided. The page and the board then
cannot disagree about what needs attention — they have diverged once before, over
how an issue is rendered.

## Consequences

A row carrying a live observation is **two lines tall**, where every other row is
one. That is the single exception to the equal-height rule in
`docs/design/po-page.md`, and it is deliberate: the thing that needs attention is
the thing that takes room. It cannot cause reflow on a healthy fleet, because a
healthy fleet has no observation to show.

The manager has to report. Nothing observes on its own, and an unreported fleet
behaves exactly as it does today — no worse, no better. That is the price of not
reading the registry, and ADR-0001 already paid it once.

An observation can be wrong. It is attributed for that reason: the row says who
saw it, and the reader weighs it. The declaration is still there, unchanged,
beside it.
