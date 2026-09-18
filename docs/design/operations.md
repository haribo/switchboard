# Running switchboard on this machine

One binary, one systemd user service, one database. No root, nothing in `/usr`.

| what | where |
|---|---|
| binary | `~/.local/bin/switchboard` |
| previous binary | `~/.local/bin/switchboard.previous` (kept by `just deploy`) |
| unit | `~/.config/systemd/user/switchboard.service` |
| settings | `~/.config/switchboard/switchboard.env` |
| database | `~/.local/share/switchboard/switchboard.db` |
| backups | `~/.local/share/switchboard/backups/` |

## Install

```bash
just install
```

Builds, installs, writes an example config **only if none exists**, enables and
starts the service, then prints `db status`. Nothing else is required: the
service is told nothing about any repository, and sessions send the full URL of
the issue they are working on.

```bash

The service runs under `default.target` with no linger: it starts when your
session does and stops when the session ends. **A dev session that publishes
while you are logged out gets a connection refused**, and says so —
`switchboard unreachable at …`. That is the trade for not leaving a service
running on a machine nobody is using.

## Branches

`develop` is where work lands. `main` carries the stable versions, and a
pre-commit hook refuses a commit made directly on it — run `just hooks` once
after cloning, or the file is just a sign with no door behind it. A build is
stamped with `git describe --tags --always --dirty`, so `just health` always says
which commit is answering — and says `-dirty` when it is not one.

## What the service says at startup

The version, the database and the batching settings — and, when there are any,
the `SWITCHBOARD_*` variables it is **not** reading:

```
no longer used, safe to delete: SWITCHBOARD_REPO (issues now carry their full URL)
not a setting, ignored (misspelled?): SWITCHBOARD_DEBOUCE
```

Two lines, deliberately apart. A setting the product dropped is a line to delete;
a misspelled one is a line to correct, and the operator would otherwise conclude
the batching is broken rather than that they typed `DEBOUCE`. The config file is
never overwritten by an upgrade, which is why a dropped setting survives in it —
so it is named rather than ignored.

It is a warning, never a refusal: a stale line must not stop a service that would
otherwise run correctly.

## Update

```bash
just deploy
```

In order, stopping at the first failure:

1. `lint` and `test` — nothing is deployed on top of a red suite;
2. build, stamped with `git describe --dirty` (or a timestamp before the first
   commit, so two builds are never confused);
3. a backup of the database, if the service is running;
4. the binary in place, the old one kept as `switchboard.previous`;
5. the unit reinstalled, `daemon-reload`;
6. restart;
7. `/v1/health` must answer, run **that** build, and report a schema that is up
   to date;
8. if step 7 fails, the previous binary goes back and the service restarts.

`just rollback` does step 8 on demand. It puts back the **binary**; it does not
undo a migration — for that, restore a backup.

## Look after it

```bash
just status     # where the database is, what schema, what it holds
just health     # what the running service says about itself
just backup     # a copy, taken while the service runs
just logs       # journalctl -f on the unit
```

`switchboard db status` works even on a database this binary is too old for —
which is exactly when somebody needs to be told.

## Adding a migration

The schema carries its version in `PRAGMA user_version`, and migrations are a
numbered list in `internal/store/migrate.go`. To change the schema:

1. **append** a migration with the next version number and a name that says what
   it does;
2. never edit one that has already run — it has shipped, and changing it would
   make two databases claim the same version while holding different shapes;
3. run `just test`: the suite migrates a populated database forward and checks
   the rows survive;
4. `switchboard db migrate --dry-run` says what would be applied, without
   applying it;
5. `just deploy` — the service migrates on startup, taking a backup first.

Each migration runs in a transaction that stamps its own version, so a database
is never half-migrated. A failure leaves it exactly where it was.

Five backups are kept, oldest pruned. They are named for the version they came
**from**: `switchboard-v2-20260917-165123.db` is what the database looked like
before moving off schema 2.

## Restoring a backup

Stop the service, move the current file aside, copy the backup into place, start
again:

```bash
systemctl --user stop switchboard
cd ~/.local/share/switchboard
mv switchboard.db switchboard.db.broken
cp backups/switchboard-v1-20260917-165123.db switchboard.db
systemctl --user start switchboard
switchboard db status
```

If the backup predates the running binary's schema, it is migrated forward on
start, with a fresh backup taken first.

## Two things the service refuses to do

**Open a database newer than itself.** An older binary writing old-shaped rows
into a new-shaped database corrupts it quietly. It stops instead and says which
schema it found.

**Open a database that has tables but no version stamp.** Those came from before
versions existed. Their shape cannot be established by looking, so they are
refused rather than guessed at — move the file aside or restore a backup.

## Development alongside the installed service

`just dev` runs on port 8799 with a database under `/tmp`, and refuses to start
if it would touch the installed one. The installed service keeps port 8787 and
`~/.local/share`. They never share a database.
