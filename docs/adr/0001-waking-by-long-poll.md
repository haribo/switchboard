# ADR-0001: the manager is woken by a long wait, not by a pushed message

## Status

Accepted.

## Context

The brief asked that the tool "send a message to the session named
`acme-manager`", leaving the mechanism open.

On this machine, Claude Code registers every session in
`~/.claude/sessions/<pid>.json`: a `name`, a `status`, and the path of a socket,
`/run/user/<uid>/cc-socks/<pid>.sock`. An outside service could therefore read
that registry to find the session called `acme-manager`, then write into its
socket.

That registry is not an interface. It is undocumented, unversioned, and its
contents change between Claude Code versions: on one machine it shows fields that
appear from one release to the next (`peerFeatures`), and a field can stop
meaning what it meant without anything breaking or warning. A tool built on it
works until the day it does not, and on that day it wakes nobody — silently,
which is exactly the failure mode we cannot afford here.

## Decision

**The service neither reads nor writes Claude Code's private registry.** It opens
no session socket and guesses no session name: a session name is always a
parameter the caller supplies.

Waking runs the other way. The service exposes a long wait, `GET /v1/wake`, which
only returns once a batch is ripe, and the manager subscribes to it through
`switchboard watch --for manager`, armed once at startup in a persistent Monitor.
One line on standard output becomes one notification, and one notification
restarts an idle session.

## Consequences

The batching — the delay, the floor, the cursor of what has already been
announced — lives on the service side, in the database. It therefore outlives the
manager session, which finds its batch again when it re-arms. That is a gain, not
a consolation: batching state held by the session would have died with it.

In exchange, the manager has to re-arm at startup. It is one line, it is in the
README, and forgetting it shows immediately — the manager stops being woken,
which `switchboard board` makes obvious at a glance.

Should Claude Code ever publish a documented, versioned cross-session send, this
choice is worth reconsidering in a new ADR. The service would only have a call to
add: what to send (one line, a count) and when (a ripe batch) is already decided
here.
