# The grouped signal

This is the point of the tool. The rest — session state, the PO's page — follows
from the brief; this needs a rule stated outright.

## The problem

An idle Claude session only picks work back up when something prompts it. If
switchboard prompted the manager on every event, the stream of text would have
moved rather than gone: five devs publishing is five interruptions.

So: **one signal**, grouped, saying that there is something — and not what. The
"what" fits in a single call (`switchboard board`) the manager makes on the way
back.

## What counts as waiting

For the manager:

- **open** events addressed to them that are not `info`;
- **unread answers** to the questions they asked themselves.

`info` is never in there. It exists to be published without anybody having to
read it now; counting it would mean waking someone for "e2e gate started, about
30 min".

The second half is the return trip, and it was the most broken part: the PO
answered, and the manager did not know.

## The rule

Three settings, and a cursor.

| setting | default | role |
|---|---|---|
| batching (`--debounce`) | 3 min | how long a new item waits before a signal goes out |
| floor (`--min-interval`) | 5 min | minimum between two signals, whatever arrives |
| urgency (`--urgent`) | 30 s | the batching delay when a `blocked` is in the batch |

A signal is owed when, **all at once**:

1. at least one item has not been announced yet;
2. the oldest of those is past the batching delay;
3. the last signal is further back than the floor.

The signal announces **everything still pending**, not only what is new: the
manager wants to know how much there is to handle, not how much arrived since
last time.

## The cursor, and why it exists

An announced batch is not a handled batch. The manager is woken, then takes a
while to come back. With no memory of what has already been announced, the same
three events would come due again as soon as the floor passed, and the manager
would be woken in a loop for what they already know about.

So the service keeps, per party, the time of the last signal and **the timestamp
of the newest item it covered**. An item at or before that cursor is already
announced. The cursor lives in the database: it outlives the manager session,
which finds its pending batch again when it re-arms.

## Urgency does not break the floor

A `blocked` shortens the batching delay; it does not lift the floor. A dev that
is actually stopped costs more than a slightly earlier signal — but never at the
price of grouping, which is the reason the tool exists. See
[ADR-0002](../adr/0002-blocked-shortens-the-batch.md).

Urgency belongs to the event, not to the queue: a plain event arriving after an
already-announced `blocked` waits the full delay.

## On the manager's side

```bash
switchboard watch --for manager
```

One long HTTP wait, looped. One line per batch, on standard output. It does not
die when the service restarts: it waits and resumes — a subscription that dies
silently stops waking anybody.

In a Claude session, arm it once at startup in a persistent Monitor. Each line
becomes a notification, and one notification is enough to restart an idle session.

## When the service falls over

A service with nothing to say and a service that is gone look alike — and the
second is an outage. So `watch` reports a lasting outage on standard output,
like any other signal:

```
switchboard unreachable for 2m — nothing is getting through
```

Once, not on every attempt; the threshold is `--down-alert`. After a recovery, a
fresh outage is announced again. Per-attempt errors stay on standard error,
where they interrupt nobody.
