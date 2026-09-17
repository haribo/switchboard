# ADR-0005: the schema carries a version number

## Status

Accepted.

## Context

Until the service was installed, the database was rebuilt whenever it got in the
way. `migrate()` reflected that: a handful of `ALTER` statements, each guarded by
looking at `pragma_table_info` to see whether the column was already there.

That works exactly as long as the database is disposable. From the moment the PO
answers a real question in it, three things break:

- **Nothing says what shape a database is in.** The only way to find out is to
  inspect every table and infer, which is guesswork dressed as a check.
- **An older binary cannot tell.** Open a database written by a newer version and
  the introspection finds the columns it knows, concludes everything is fine, and
  starts writing old-shaped rows into a new-shaped database. Quietly.
- **It does not scale.** Every future change would need its own idempotence test,
  and the file becomes a pile of conditionals nobody can read as a history.

## Decision

`PRAGMA user_version` holds the schema version, and `internal/store/migrate.go`
holds an **ordered list of numbered migrations**. Opening a database applies
whatever is missing, each migration in a transaction that stamps its own version.

Three rules follow:

- **A migration that has shipped is never edited.** It has already run somewhere;
  changing it would make two databases claim the same version with different
  shapes. Changes are appended.
- **A database newer than the binary is refused.** Not migrated backwards, not
  opened read-only — refused, with a message naming both versions and where the
  backups are. Writing into it is the failure this whole decision exists to
  prevent.
- **A database with tables but no version is refused too.** It predates this
  mechanism, its shape cannot be established, and guessing is what we are getting
  rid of.

A backup is taken before any migration, with `VACUUM INTO` rather than a file
copy: under WAL the file on disk is missing whatever sits in the write-ahead log,
so copying it yields a database that is silently behind — the worst kind of
backup. Five are kept.

The schema as it stands becomes **migration 1**, in one block. The `ALTER`
statements from the construction (`text` into `title`/`body`, `ticket` to
`issue`, `actionable_at`, `role`) are dropped rather than replayed: they never
left this machine, and keeping them would pass groping for history.

## Consequences

Changing the schema costs one appended entry and one test — the suite migrates a
populated database forward and checks the rows survive. That is the price of
knowing, at any moment, what a database holds.

Rolling a deployment back puts the **binary** back, not the schema. If the new
version migrated the database, the old binary will refuse to open it, on purpose:
the backup taken before the migration is the way back, and restoring it is a
deliberate act. An automatic down-migration would have to guess how to undo a
change that has already been written to, which is the same guesswork under
another name.
