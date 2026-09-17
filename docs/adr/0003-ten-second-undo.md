# ADR-0003: an answer can be taken back for ten seconds

## Status

Accepted.

## Context

The PO's page answers in one click on an option. That is what makes it fast, and
it is also what makes the wrong click easy: two buttons side by side, "Save" and
"Confirm", and the wrong one goes out.

Three routes were considered.

**Confirming before sending** prevents the mistake but doubles every gesture —
over a day of asks it costs more than it saves, and the brief describes a PO who
answers quickly and returns to their own work.

**A window of several minutes** catches comfortably, but it changes in kind: past
a few seconds it is no longer a misclick being corrected, it is an opinion being
revised. And a dev leaves with the answer the moment it is written. The longer
the window, the likelier the withdrawal lands after work has been done on it.

## Decision

**Ten seconds**, tunable with `--undo-window`.

The answered card does not disappear: it folds into a strip, **in place**, with
the answer given, an "Undo" button and a countdown. It clears when the window
closes. Nothing above it moves while it is there.

The server holds the window. The page only shows what the server tells it: a
reload does not cost the chance to undo, and a page left open cannot offer an
undo the server will refuse. Only the author of an answer can take it back.

## Consequences

A reopened event becomes **actionable now**, not at its creation time. Without
that, the batching would have treated it as already announced and nobody would
have been told it is waiting again. That is what the `actionable_at` column
carries: the date the batching measures, distinct from the creation date.

If the asker had **already read** the answer, the withdrawal cannot be silent:
someone is working on a decision that has just been taken away. It then raises an
ask to the manager, naming the dev to reach and the issue at stake. The manager
reaches the dev — the tool never talks to a session, see
[ADR-0001](0001-waking-by-long-poll.md).

Ten seconds does not catch a change of mind. That is deliberate: an opinion that
changes afterwards is a new decision, it goes through the manager like any
decision, and it leaves a trace instead of erasing one.
