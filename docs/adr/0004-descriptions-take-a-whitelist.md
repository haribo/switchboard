# ADR-0004: a description is HTML, from a short whitelist

## Status

Accepted.

## Context

An ask needs more than a line. "Sign-in screen, second pass" says what it is
about; what changed, and where to look, needs a paragraph, a list, and above all
a **link**. Without a clickable link inside the text, half the asks make the PO
copy a URL by hand — which is the friction the tool exists to remove.

So descriptions carry markup. The question is which.

Rendering whatever a session sends is the obvious route and the wrong one. The
text comes from a session that sometimes relays writing from elsewhere — an issue
comment, a log line, a file it did not write. It lands on a page where the PO
clicks, on an origin that holds the service's own API. A `<script>` in a
description is a script running with the PO's page.

Escaping everything is the other extreme, and it removes the link that motivated
the feature.

## Decision

A **whitelist**, applied server-side on the way in, never on the way out:

- `p`, `br`, `strong`, `b`, `em`, `i`, `code`, `ul`, `ol`, `li`;
- `a` with `href`, limited to `http`, `https` and `mailto`.

Anything outside the list is kept **as text**, not dropped: a description is
never silently emptied, and the reader sees what was written even when the markup
did not survive.

Fully qualified links leave with `target="_blank" rel="noreferrer"`, so the PO's
page is never the thing that gets replaced.

The sanitizing happens once, at `POST /v1/events`, and what is stored is already
clean. A stored value can then be trusted by every reader, instead of each of
them having to remember.

## Consequences

The service depends on `bluemonday` rather than a hand-written filter. Writing
one's own HTML sanitizer is a well-known way to be wrong quietly; this one is
small, focused and exercised.

The page sets the description with `innerHTML`, which is safe only because of the
rule above. That coupling is stated here and in the code: anything that ever
stores a description without passing it through `richtext.Clean` breaks the page's
safety, not just its formatting.

Titles take no markup at all. A title is one line the PO skims; formatting there
would be noise, and the catch-up events the service composes itself escape what
they interpolate.
