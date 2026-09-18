# switchboard

<https://github.com/haribo/switchboard>

Five Claude sessions build in parallel, a sixth hands out the work, and a PO
decides. The devs' status messages landed in the manager's terminal — which is
where the PO was reading. Text scrolled, the PO's question disappeared under it.

switchboard carries the three things GitHub cannot say:

- the living state of a session — working, waiting, blocked, idle;
- an ask addressed to a human, typed and followed through to its answer;
- a place where the PO answers without a stream of text running over them.

Issues, labels and pull requests stay on GitHub. switchboard duplicates none of it.

## Install

```bash
just install
```

Binary into `~/.local/bin`, a systemd **user** service enabled and started, and
an example config at `~/.config/switchboard/switchboard.env`. No root, nothing in
`/usr`. The service listens on `http://127.0.0.1:8787` and keeps its database in
`~/.local/share/switchboard/switchboard.db`. No authentication: it runs on the
machine, for the sessions on it.

**Issues are given as their full URL:**

```bash
switchboard state --session acme-dev3 --status active \
    --issue https://github.com/acme/app/issues/142
```

The page shows `#142` and links it. A bare number is refused — the service holds
no repository to resolve it against, and will not be given one: a number resolved
against a repository the session was not working in links to somebody else's
issue, which is worse than no link. A session knows its own repository, so
sending the whole address costs it nothing.

The service follows your login session — it starts with it and stops with it. A
dev session publishing while you are logged out gets a clear
`switchboard unreachable at …` rather than silence.

Updating is `just deploy`: checks, backup, swap, restart, verify, and it puts the
previous binary back if the new one does not come up. See
[docs/design/operations.md](docs/design/operations.md).

Clients target `http://127.0.0.1:8787` by default, or `$SWITCHBOARD_URL`.

## The API

The CLI below is a convenience; every command is one HTTP call, and a session can
make those directly. The contract is OpenAPI 3.1, served by the service itself:

```bash
curl http://127.0.0.1:8787/v1/openapi.yaml
```

Two calls block — `GET /v1/events/{id}/reply` and `GET /v1/wake`. They take a
`wait` in seconds, capped at 600, and answer `204` when it runs out. A caller
that uses them raw has to loop; `switchboard await` and `switchboard watch` do
that for you.

## For a dev session

```bash
# where I am
switchboard state --session acme-dev3 --status active --issue 142 \
    --detail "action bar, label of the primary button"

# something nobody has to act on
switchboard event --from acme-dev3 --kind info --title "e2e gate started, about 30 min"

# a call I cannot make alone — and I wait for it
id=$(switchboard event --from acme-dev3 --kind question --issue 142 \
       --title "Label of the primary button in the action bar" \
       --body '<p>It has said <code>Save</code> from the start. The <a href="…">mockup</a> says "Confirm".</p>' \
       --option Save --option Confirm)
switchboard state --session acme-dev3 --status waiting --issue 142
switchboard await "$id" --timeout 2h

# a look from the PO — a link is required
switchboard event --from acme-dev3 --kind validation --issue 142 \
    --title "Sign-in screen, second pass" --link http://localhost:5173/login

# I am actually stopped
switchboard event --from acme-dev3 --kind blocked --title "Migration failing on the test database"

# never mind — I found the answer myself
switchboard withdraw "$id" --as acme-dev3

# I am done for good: take my line off the table (my events stay)
switchboard retire acme-dev3
```

```bash
# the PO could not act on how I worded it — say it again, plainly
switchboard explain "$id" --as acme-dev3 --body "<p>Which way the export fetches page 2.</p>"
```

`await` stops on three outcomes, and exits differently for each so a session can
branch without reading prose: **0** an answer, **3** the ask was withdrawn, **4**
the PO asked for it to be put in plain words — reword it and await again.

`--wait 2h` on `event` publishes and waits in one go.

**Title and body** split two readings: the title is what the PO skims to decide
whether to take this one now, the body is what they read once they have. A title
is one line, 120 characters at most. A body may carry links, bold, italic,
`code`, lists and paragraphs; anything else shows as plain text.

## For the manager

```bash
switchboard board              # everything, in one call
switchboard reply 12 --text "ISO 8601, always"
switchboard ask --title "Do we start phase 2 on Thursday?" --option "Phase 2" --option "Debt first"
switchboard ask --forward 12   # hand a dev's question to the PO
switchboard ack                # "read": the info and the PO's answers
switchboard undo 12 --as manager
switchboard withdraw 12 --as manager   # close an ask that no longer needs an answer
```

### The grouped signal

An idle session only picks work back up when something prompts it, and one
signal per event would rebuild the original defect. The service batches, and the
manager subscribes:

```bash
switchboard watch --for manager
```

It says nothing while there is nothing, then prints **one line**:

```
3 events to handle — `switchboard board`
```

A count, never the content. In a Claude session, arm it **once at startup** in a
persistent Monitor on that command: each line becomes a notification, and one
notification is enough to restart an idle session.

If the service becomes unreachable, `watch` says so — once, on the same output,
after two minutes. Silence that looks like "nothing to do" would be an invisible
outage.

Defaults: 3 minutes of batching, 5 minutes minimum between two signals, 30
seconds when a dev is blocked. See [docs/design/waking.md](docs/design/waking.md).

## For the PO

One page, kept open: <http://127.0.0.1:8787>.

**Sessions** is a table: dev, state, issue, what's happening, and for how long.
The state is an icon with a legend under the table, and two of the four are
worked out by the service rather than declared — *waiting on you* and *blocked*.
Rows are always in name order, so every dev keeps the same line all day.

**Your call** is what is addressed to the PO, and nothing else. Each ask carries
who is asking, a title, a description, the offered options as buttons, and a free
text field.

Nothing scrolls: an ask being typed into is never redrawn, the order never moves,
and if the service falls over the page says so instead of looking empty. The
browser tab carries the count — "(3) switchboard" — and that is the only thing
that calls out.

An answer can be taken back for **ten seconds**. The card holds its place, folded
into a strip with the answer, an "Undo" button and a countdown. If the dev had
already read the answer, the withdrawal is raised to the manager, who reaches
them. See [ADR-0003](docs/adr/0003-ten-second-undo.md).

## Development

```bash
just test      # the suite
just race      # with the race detector
just lint      # gofmt + go vet
just dev       # port 8799, a database under /tmp, short delays — never the installed one
just status    # where the installed database is, what schema, what it holds
just health    # what the running service says about itself
just logs      # journalctl -f on the unit
```

- **The API contract** — [internal/api/openapi.yaml](internal/api/openapi.yaml),
  served by the service at `/v1/openapi.yaml` and `/v1/openapi.json`. A session
  that has the service has the contract; a test compares it to the router in both
  directions, so it cannot drift.
- [docs/design/waking.md](docs/design/waking.md) — batching, in full
- [docs/design/po-page.md](docs/design/po-page.md) — what the page shows and why
- [docs/design/operations.md](docs/design/operations.md) — install, update, roll back, add a migration
- [docs/git.md](docs/git.md) — branches, commit messages, what goes in one commit
- [docs/adr/](docs/adr/) — the decisions, and what each one ruled out
