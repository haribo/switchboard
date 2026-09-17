# ADR-0002: a blocked dev shortens the batching delay, without lifting the floor

## Status

Accepted.

## Context

The four kinds of event do not cost the same to leave waiting. A `question` can
ripen for three minutes without harm: the dev has other things to do. A `blocked`
says a session has stopped — every minute of batching is a minute of work lost,
and there are five sessions.

The temptation is to treat `blocked` as a full exception: signal at once, no
delay, no floor. That is the door the original defect walks back through. Four
devs blocked on the same migration, and the manager takes four interruptions —
precisely the situation the tool removes.

## Decision

An unannounced `blocked` **shortens the batching delay** (30 s instead of 3 min,
`--urgent`).

It **does not lift the rate floor**. If a signal has just gone out, the `blocked`
waits like everything else.

Urgency belongs to the event, not to the queue: a plain event arriving after an
already-announced `blocked` waits the full delay. The queue does not stay "hot"
because it once held an urgent item.

## Consequences

The worst case for a blocked dev is the floor — five minutes by default: bounded,
known, tunable. In exchange the number of interruptions the manager takes stays
bounded whatever happens, which is the property the tool must guarantee.

The signal says how many of the batch are blocking ("3 events to handle, 1
blocked"), so the manager knows whether to come straight back without being sent
the content.
