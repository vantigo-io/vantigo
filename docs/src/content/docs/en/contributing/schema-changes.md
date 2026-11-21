---
title: Schema changes
description: The one-release compatibility rule for migrations — the schema a release migrates to must work with the previous release's binary — how a change that removes or narrows is split into expand and contract, the statements the migration check fails on, the `-- vantigo:contract` marker, and why the old pods of a rolling upgrade run `server` and never `api`.
sidebar:
  order: 8
sources:
  - tools/db
---

Vantigo upgrades without downtime. A release's migrations run once, as the `migrate`
command (a Kubernetes Job, or Compose's `vantigo-migrate`), and then the new pods replace
the old ones a few at a time. Until the last old pod has stopped, **the previous release's
binary serves requests and runs workers on the new schema**. Every migration is written
for that moment.

## The rule

**The schema a release migrates to must work with the previous release's binary.** The
previous release must still be able to read every row it reads, and write every row it
writes, after the migration has run.

Adding is safe: a new table, a nullable column, a column with a `DEFAULT`, an index, a
function, or a constraint the old binary's writes cannot break. Removing or narrowing is
not: the old binary still selects the dropped column, still inserts without the newly
required value, still writes the value the new `CHECK` refuses.

A change that removes or narrows is therefore split across two releases:

1. **Expand**, in one release: add the new shape next to the old. The code writes both
   and reads the new. A backfill brings the existing rows across.
2. **Contract**, in the next release: once no binary that knows only the old shape can
   be running, drop the old column, set the `NOT NULL`, add the constraint. The contract
   migration carries the marker below.

Renaming a column, for example, is three steps over two releases: add the new column and
write both (expand); backfill and switch the reads in the same release; drop the old
column in the next (contract). Migrations stay forward-only: goose's `-- +goose Down`
section is never what an upgrade relies on.

## The migration check

`tools/db/check-migrations.ts` reads the migrations a branch adds or edits under
`apps/server/internal/db/migrations/` — compared with its merge base against `main`, so
the migrations already on `main` are not read again unless the branch changes them — and
fails on each statement an old binary can trip over:

| Statement | Why it breaks the previous release |
| --- | --- |
| `DROP TABLE`, `DROP SCHEMA`, `DROP VIEW`, `DROP MATERIALIZED VIEW`, `DROP FUNCTION`, `DROP PROCEDURE`, `DROP TYPE`, `DROP DOMAIN`, `DROP SEQUENCE` | The old binary still queries or calls it. |
| `ALTER TABLE … DROP [COLUMN]` | The old binary still selects or inserts the column. |
| Any `RENAME` (table, column, constraint, index, type, enum value), and `SET SCHEMA` | The old binary uses the old name. |
| `ALTER COLUMN … TYPE` | The old binary's queries and scans expect the old type; a narrowing also rewrites the table under a lock. |
| `ALTER COLUMN … SET NOT NULL` | The old binary inserts rows without the value. |
| `ALTER COLUMN … DROP DEFAULT` | The old binary relies on the default when it omits the column. |
| `ALTER COLUMN … ADD GENERATED ALWAYS` | The old binary's inserts that name the column are refused. |
| `ADD COLUMN … NOT NULL` without a `DEFAULT` (or with `DEFAULT NULL`) | The old binary's inserts do not name the column. |
| A new `CHECK`, `UNIQUE`, `PRIMARY KEY`, `EXCLUDE` or foreign-key constraint on an existing table | The old binary's writes may violate it. |
| A new unique index on an existing table | The same, as an index. |
| A new trigger on an existing table | It runs on the old binary's writes, which it was not written for. |

Only the `-- +goose Up` section is read. Comments, string literals and the bodies of
functions are ignored; the body of a `DO` block runs during the migration, so it is read.
What the same branch does to a table it creates itself is not checked: no released binary
knows that table yet. A `CHECK` or foreign key on columns the branch adds to an existing
table passes too, since an old insert leaves those columns `NULL` or at their `DEFAULT`; a
`UNIQUE` on them does not, since every old insert gets the same default. A `CREATE TABLE
IF NOT EXISTS` or `ADD COLUMN IF NOT EXISTS` may name what already exists, so it counts
as neither new nor added.

Some statements are warnings rather than failures — annotated on the pull request for the
reviewer, without failing the check:

- A `CHECK` that replaces a constraint of the same name the branch drops, which is how a
  list of allowed values grows. The check cannot tell a widening from a narrowing, so the
  reviewer confirms that every value the previous release writes still passes.
- A dropped index or constraint the branch does not re-create under the same name. Dropping
  widens, but a unique index or constraint may be what the previous release's
  `ON CONFLICT` resolves against.
- `EXECUTE` in a `DO` block: dynamic SQL, which the check cannot read.

The check runs in `mise run server:check` and in CI's Go server lint job on every pull
request. Run locally, it reads the branch's commits, so commit a new migration before you
expect it to be checked. To check particular files, committed or not, pass them:

```sh
bun run tools/db/check-migrations.ts --files apps/server/internal/db/migrations/00220_example.sql
```

## The contract marker

A contract migration — the second release of an expand-and-contract change — opts out of
the check with a line anywhere in the file:

```sql
-- +goose Up
-- vantigo:contract drops invoices.legacy_kid; 2026.11 stopped reading and writing it
ALTER TABLE invoices.invoices DROP COLUMN legacy_kid;
```

The reason is required; a marker without one fails. The marker waives the whole file, so
keep a contract migration to the contract statements alone. The reviewer checks the
reason against the previous release: the expand step has shipped in a release, and that
release no longer touches what the contract removes or narrows.

## Old pods run `server`, never `api`

The `api` command migrates before it serves. An old `api` started during a rolling upgrade
would ask goose to migrate a database that already carries versions it does not know. So
the pods that keep serving during an upgrade run `server` (and `worker` for the
background workers), which never migrate, and the new release's migrations run once as
the `migrate` command before the new pods start. The single-container Compose stack runs
`api`, but replaces its one container rather than rolling a fleet: the old container is
never started again on the new schema. See
[What the stack contains](/en/admin/installation/#what-the-stack-contains) for what each
command does.

## What the check does not cover

The check reads statements, not intent, and it compares with `main`, not with the last
release. A data migration that rewrites values the old binary still writes, a new value in
a column the old binary parses into a closed set, a change to what a function returns, a
new table whose foreign key keeps the old binary from deleting a parent row, row-level
security, or a detached partition pass it and still break the previous release. So does a
contract step whose expand step is on `main` but not yet in a release. The
review asks the same question of every migration: can the previous release's binary keep
working on this schema?
