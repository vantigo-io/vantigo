# Communications module — Go port design (sub-project 5)

Implements sub-project 5 of [the Go backend port](2026-09-10-go-backend-port-design.md).
Behavioural authority is [the communications inventory](2026-09-13-communications-inventory.md);
where this document and the inventory disagree, the inventory is right about what .NET
does and this document is right about what we build.

**Goal.** Port the Communications module to Go, single-tenant, outbound-only, and add
the three platform pieces it needs: an object-storage port with an `fs` driver, a
`Worker` port with a runner, and the outbox.

## 1. Scope

Kept (parent §9): channels (SMTP only), conversations, messages, the composer,
attachments on `fs` storage with the same size limits, suppressions, retention, the
outbox and its three workers, the AI draft and customer-suggestion feature, and the
`Vantigo.Communications` metrics. 28 contract operations, five permissions.

Removed (parent §9): the Mailgun outbound provider, the Mailgun inbound webhook and
its signature verification, MIME building, inbound HTML sanitisation and the inbound
worker, the ClamAV client and the scanner worker. Tenancy is dropped throughout.
`channels.kind` survives so a future non-SMTP provider is additive.

### 1.1 The consequence of removing inbound, stated plainly

The reply path resolves recipients from the latest **inbound** message's participants,
and the inbound worker was that message type's only producer. IMAP and inbound
synchronisation were never implemented. So after this port there is no inbound path at
all, and three things follow:

- A conversation created through `POST /conversations` is permanently unrepliable:
  reply answers 422 `recipients_missing`.
- `replyAllCc` is permanently empty and `canReplyAll` permanently false.
- The AI draft builds its context from inbound messages, so it drafts against an empty
  context and records `product_data=false`.

This is what the parent spec chose — §2 says "Outbound SMTP only" and §9 says
"inbound-only UI states are simply never reached" — so we port these endpoints
faithfully, keep them contract-complete, and document the limitation here, in the
inventory's hazards section and in `CONTRIBUTING.md`. We do not invent an inbound path,
and we do not delete endpoints the contract declares.

**Sharpened (2026-09-13, from Task 7).** "Reply is unusable" understates it. **No production
path writes an inbound message** — the inbound worker was the only writer and it is out of
scope — so `recipients_missing` fires at step 7 of reply's ten-step order and steps 8, 9 and
10 never run **in production**: the doubly-enforced `attachments_not_ready` gate and the
`recipient_suppressed` check are dead through the API. Two consequences worth stating for
whoever picks this up: attachments can be staged but never sent, and suppression is enforced
only by the outbox worker's own checks, not by reply.

**Correction (2026-09-13, Task 7 fix round 2).** An earlier version of this section, and
Task 3's schema ruling behind it, went further: the `direction` CHECK was narrowed to
`('outbound', 'internal_note')` on the principle that the schema should encode only what the
port can produce. That was wrong, and the bill came due twice. It made the real
recipient-resolution query permanently unexercisable — no fixture could insert an inbound
row, so no test could drive the query's success branch, and a seam added to compensate
merely relocated the blindness. And it would have required a migration on the day an inbound
provider landed. The CHECK now matches .NET's own set, **including `inbound`**. Nothing in
this port writes such a row, so the outbound-only reality above is unchanged; what changes is
that the schema stops asserting a restriction .NET never made, fixtures can exercise the real
query and reply's full order, and the inbound provider arrives without a schema change.

## 2. Platform additions

**`internal/storage`.** An `ObjectStore` port with `Put`, `Get`, `Exists`, `Delete`,
and an `fs` driver. Keys are `{scope}/{relative-key}` — the tenant segment of .NET's
`tenants/{tenant-id}/{scope}/{key}` is dropped with tenancy. Scope names are canonical
lowercase `[a-z0-9-]`; communications passes only relative keys. The driver rejects
traversal, URL-encoded, absolute, backslash, control-character and duplicate-prefix
keys before every operation; writes through a temporary file under the root followed by
an atomic replace; never returns a physical path; refuses a root that is a symlink or
group/world-writable outside development; and fails closed when unconfigured. There is
deliberately no presigned-URL operation: downloads stream through an authorised
application endpoint.

**`Worker` and the runner.** A `Worker` is `Run(ctx) error` with a poll interval. The
runner starts every enabled module's workers as goroutines in `worker` mode and in
`api` mode when `WORKERS_IN_PROCESS=1`, never in `server` mode. No `Worker` type exists
in Go today; `cmd/vantigo`'s `worker` mode currently serves only health.

**The outbox.** Delivery semantics are preserved exactly as the code implements them
(see §4).

## 3. Module shape

Migration `00006_communications_baseline.sql` owning schema `communications`: 19 tables
— the inventory's 21 minus `inbound_receipts` and `inbound_email_jobs`, with
`message_attachments` and `attachment_uploads` each losing five of their six scan columns
(`scan_attempts`, `next_scan_at`, `scan_lease_id`, `scan_lease_until`, `scan_error`) and
keeping only `scan_status`, which is contract-visible and gates replies — see D4's
correction — and `conversation_messages` keeping `raw_payload_storage_key` and its
`direction` column, whose CHECK matches .NET's full set **including `inbound`** even though
no production path writes that value (see §1.1's correction: narrowing it made the real
recipient query unexercisable and would have forced a migration when inbound lands).

sqlc queries, a module skeleton in the shape Tasks 5/10/13 established, and the
five-permission catalog verbatim from `CommunicationsPermissionCatalog.cs`, with
`conversations-view` marked **sensitive**. Access is contract-driven: `module.Router`
enforces `x-vantigo-access`, and no handler re-checks what the router checked.

**Error shapes.** Two vocabularies coexist in .NET and both are observable, so both are
ported: every module error is `{error:{code,message,fields?}}`, *except* the three stats
endpoints, which emit RFC 7807 `ProblemDetails`. There is no
`HttpValidationProblemDetails` anywhere and all 404s are bare-bodied.

**Ordering.** The per-endpoint validation-versus-existence order is deliberately
inconsistent and is ported endpoint by endpoint from the inventory, including the two
handlers that split their validation around the 404 in opposite directions
(`UpdateChannel`, `PATCH /conversations/{id}`) and the two idempotency asymmetries
(reply puts the inactive-channel 422 ahead of the replay; staging puts the replay ahead
of existence).

## 4. Outbox and workers

Claim by conditional update with a lease; `attempts` increments **at claim**, so
terminality is `attempts >= max_attempts` post-increment. The `delivery_attempted_at`
marker is cleared on claim and committed in its own write immediately before the
external send; a job re-claimed with it set is logged and counted as a possible
duplicate. `Message-Id` is deterministic from the message id, so a resend carries the
same identity. Backoff is `min(3600, 2^min(attempts, 10))` seconds.

Three workers: outbox delivery, retention, attachment cleanup. **Only retention takes
the advisory lease**; delivery and cleanup rely on conditional-update claims. Retention
deletes `message_events` **before** `message_deliveries` — the schema's only `RESTRICT`
foreign key dictates that order and reversing it makes retention fail permanently.

## 5. Decisions

**D1 — `IProductCatalog` stays out of scope.** .NET resolves it with `GetService` and
returns `(Used:false, Text:"none")` when absent, sending `products: none` in the prompt
and `product_data=false` in the interaction. Absence is an already-exercised path, not a
degradation we invent, so sub-project 4's ruling stands and a Go `contracts.ProductCatalog`
remains additive.

**D2 — new attachment uploads are created `clean`.** The `scanStatus=clean` gate is
load-bearing in four places, and with the scanner removed nothing would transition
`pending → clean`, so every attachment reply would 409 `attachments_not_ready` forever —
while §9 explicitly keeps attachments. Uploads are therefore created `clean` because
there is nothing to scan. The column, the contract's `scanStatus`/`ready` fields and the
gate code all stay, so the frontend is unchanged and a future scanner is additive.

The inventory's §19.4 records that one pass framed this as three options and another as
four, the extra being **removing `scanStatus` from the contract**. That fourth option is
rejected: the field is `required` in two response schemas, the frontend reads both
`scanStatus` and `ready`, and removing it would be a breaking contract change plus a
frontend change in a sub-project whose stated aim is that "the Communications frontend
keeps working". Keeping the field costs one column and buys a scanner that can be added
later without touching the contract. This closes §19.4.

**D3 — the two behaviours living inside the deleted scanner are re-homed to retention.**
`ExpireUploadsAsync` enforces the 24-hour staged-upload expiry and enqueues expired
objects for deletion; `ClamAvOptions.MaxBytes` carries the 10 MiB per-file limit. Expiry
moves to the retention worker, which already drives durable object deletion through
`attachment_cleanup_records`; the size limit moves to communications config.

**D4 — DDL defaults are added to match the C# property initialisers.** Only three real
defaults exist in .NET (`channels.provider='smtp'`, `is_default=false`, `is_active=true`);
`conversations.status='open'`, `message_deliveries.status='queued'`,
`outbox_jobs.status='pending'`, `attachment_cleanup_records.status='pending'`,
`conversation_participants.role` and `scan_status` are initialisers on `NOT NULL` columns
with no DDL counterpart. The initialiser *is* the effective default, so we encode it in
DDL. This cannot change observable behaviour — every write supplies the value — and it
removes a class of NOT NULL failures in raw inserts. Recorded as a divergence.

**Correction (2026-09-13).** This list originally also named `next_scan_at`, and §3 above
originally said both attachment tables lose "their six scan columns each" while D2 keeps
`scan_status` — a contradiction, since a column cannot be both dropped and defaulted. It
was copied from the schema inventory without filtering for what survives the scope cut.
Settled: of the six scan columns, **only `scan_status` survives** — it is contract-visible
and gates replies. `next_scan_at` is dropped along with `scan_attempts`, `scan_lease_id`,
`scan_lease_until` and `scan_error`, because the inventory establishes it is "still written
once (staging, entity default) but never advanced and never read", and its only index is
the scanner queue index, which dies with the scope cut. §3's wording below is corrected to
match.

**D5 — `conversation_customer_candidates` keeps its table and read paths** even though
its only writer (the contact linker, reached only from the inbound processor) is gone.
Dropping it would change response shapes the frontend reads, for no benefit, and it
becomes live again when an inbound provider returns.

**D6 — the index on the conversations feed is `(status DESC, last_activity_at DESC)`.**
The .NET index is `(tenant_id, status, last_activity_at)` with positional descending
flags `{false, true, true}`; dropping the ascending leading column leaves both remaining
columns descending. The natural mistranslation `(status, last_activity_at DESC)` is wrong.

**D7 — both email normalisations are ported exactly as they are.** Suppression
normalisation **uppercases** and that uppercased value is stored and echoed in
suppression responses, `recipient_suppressed` fields, `replyTo` and `replyAllCc`; the
contact linker lowercases. The asymmetry is faithful, and the inventory records it.

## 6. Deliberate divergences from .NET

1. New attachment uploads are `clean` rather than `pending` (D2), because the scanner
   that would have advanced them is removed and attachments are in scope.
2. The staged-upload expiry and the 10 MiB attachment limit move from the scanner to
   retention and to config (D3).
3. DDL defaults are added for columns whose only default was a C# initialiser (D4).
4. `docs/communications.md` is corrected in two places where it disagrees with the code:
   the outbox backoff cap is documented as 3600 s but `2^min(attempts,10)` clamps the
   real ceiling to 1024 s, and with `max_attempts = 8` the largest live backoff is 128 s;
   and the advisory lease is described as protecting every queue when in fact only
   retention takes it. The parent design's §3.10 inherits the same overstatement and is
   corrected with it.
5. The Mailgun and scanning sections of `docs/communications.md` are removed, since the
   endpoints and workers they document no longer exist.
6. Rejecting a non-SMTP channel provider reports `SMTP credentials require the smtp
   provider.` rather than .NET's `Provider must be smtp or mailgun.` The .NET message
   names a provider this port does not implement, so repeating it would advertise a
   capability that does not exist; the replacement is itself verbatim .NET text from the
   same validator, so no wire string is invented. `channels.provider` is deliberately left
   without a CHECK constraint so a future provider stays additive, which is why the
   handler also keeps an unreachable defensive 422 for a non-SMTP row.

**Added 2026-09-13 (task 14).** Six more, ruled during implementation and not written down
until the module was composed:

7. **Outbox tuning is frozen as package constants** (`outboxPollInterval`, `outboxLeaseDuration`,
   `outboxClaimAttempts`, `outboxMaxAttempts`), where .NET reads `Outbox:ClaimAttempts` and
   `Outbox:MaxAttempts` from configuration (inventory `:1271`, `:1333`). §7 above enumerates this
   module's configuration and lists no `Outbox:*` key, so the choice is between four
   un-exercised environment variables and four constants carrying .NET's own defaults. A
   deployment that needs to tune one gets a config key on the day it needs it.
8. **Configuration rejects out-of-range values where .NET clamps them**: `_DAYS` to `[1, 36500]`,
   `_BATCH_SIZE` to `[1, 1000]`, `_POLL` to positive. .NET silently applies `max(1, days)` and
   `Math.Clamp(value, 1, 1000)`, so a typo becomes a retention window nobody asked for; here it
   is a startup error naming the variable. **The 36500 upper bound is this port's own invention**
   — .NET has no upper bound on retention days at all — chosen because a hundred years is past
   any real policy and a five-digit typo is not.
9. **The AI customer-suggestion path answers 200 on a provider outage**, asymmetric with draft's
   422 for the identical failure. This is genuine .NET behaviour that inventory §17.4 item 10
   calls apparently unintended: the endpoint's guard chain tests three outcomes and a final
   `Outcome == "invalid"`, none of which `"failed"` matches, so control falls through to
   `TypedResults.Ok`. Ported faithfully and pinned by a test that fails if someone "harmonises"
   the two.
10. **Retention's orphan sweeps skip empty batches.** .NET runs both full-table anti-joins on
    every batch including the empty last one of each drain; this port returns early. Safe because
    retention is the only producer of those orphans — a batch that deleted no message created no
    orphan, and the batch that did delete already swept after itself.
11. **Retention's expiry sweep drains rather than running one fixed page.** The .NET constant it
    inherited was a transaction bound whose throughput implication did not survive the move to a
    60-minute cadence.
12. **The default `MODULES` set includes `communications`.** Until this task the name parsed but
    mounted nothing, and the list deliberately omitted it; now that `Module()` exists there is
    nothing to omit. A deployment that does not want the module names the others explicitly.

**Divergences introduced by task 14's unique-constraint audit.** Both are single-statement changes
that make a concurrent path safe without changing any sequential behaviour:

13. **`InsertParticipant` carries `ON CONFLICT (channel_id, address) DO UPDATE`** where .NET does a
    bare `Add`. The lookup-then-insert in `findOrCreateParticipantByAddress` is .NET's own shape and
    its own race; the difference is that .NET's loser throws into an unmapped 500 while this port's
    loser would have produced a bare RFC 7807 409 in the wrong vocabulary and rolled back its entire
    conversation-create transaction. The `DO UPDATE` writes the conflict key back to itself and
    leaves `display_name` and `contact_id` untouched, preserving .NET's rule that an existing
    participant is never reassigned.
14. **`ClearOtherDefaultChannels` drops the `is_default` term from its `WHERE`.** With it, two
    concurrent "make me the default" **updates** each fail to see the other's uncommitted row under
    READ COMMITTED and collide on `ux_channels_type_is_default` — a 23505 that
    `putCommunicationsChannelsById`'s contract (200/400/401/403/404) has no status to carry. Without
    it the statement visits, blocks on and re-checks the winner's row, and that pair resolves as
    last-writer-wins. The cost is writing `false` over rows that already hold it, which widens the
    statement's write set from "the current default" to "every other channel row".

    **Scope, corrected:** this closes the **PUT-vs-PUT pair only**. An earlier version of this entry
    said the race resolves "with no violation at all", which was true of the pair that had been
    tested and false in general — see 15.

15. **Both channel write paths take a type-keyed transaction advisory lock
    (`lockDefaultChannelSlot`) before demoting.** The PUT-vs-POST pair cannot be fixed by any
    statement: when an update demotes, the row a concurrent create is about to insert **does not
    exist yet**, so no `WHERE` clause can visit it, `EvalPlanQual` has nothing to re-check, and
    `SELECT ... FOR UPDATE` has nothing to lock. The update sets its own row true and collides.
    The two alternatives were rejected deliberately — catching the violation would require adding a
    409 to an operation whose contract declares none (a wire-contract change, and a divergence from
    .NET), and row locking cannot reach a nonexistent row — so the writers are serialised instead,
    which preserves the contract exactly: callers still observe last-writer-wins, never a conflict.
    The lock uses the two-int32 overload under its own class constant, keyed by channel **type**
    (the index is per type), so it shares no key space with retention's single-bigint lease and two
    types never wait on each other. .NET has no such lock, which is what makes this a divergence
    rather than a fidelity fix; .NET simply has the defect.

    **Consequence worth recording: it makes task 3's `default_channel_conflict` 409 unreachable.**
    Serialising the writers means concurrent creates now behave exactly as sequential ones do — each
    one's "is there already a channel?" read and its demote run after the previous writer committed —
    so the collision that 409 was invented to report no longer occurs. Concurrent creates all
    succeed and the last writer holds the default slot. This is strictly better for the caller (it
    gets the channel it asked for rather than a retry-me conflict) and matches what sequential
    callers always saw, but it is a **behavioural change to a documented refusal**, not merely an
    internal one. The guard itself is deliberately kept as a defensive backstop: the unique index
    remains the real invariant, and a future write path that forgets the lock must not resurface the
    bare RFC 7807 409.

**A note on numbering, because this list is meant to be cited.** Entries 1–6 predate task 14 and are
unchanged. Task 14 added 7–12 (12 is the `MODULES` default), then 13–15 from its constraint audit. The
bracketed inline `Content-ID` change is **not** a numbered divergence — it is recorded in the fidelity
fixes below, because it removes a difference from .NET rather than adding one. An earlier dispatch
referred to it as "item 12"; the committed numbering here is what should be cited.

**Fidelity fixes, recorded so they are not mistaken for divergences.** Each removes a difference from
.NET rather than adding one: inline `Content-ID` is bracketed to match MimeKit (§15.5; go-mail writes
the value verbatim, and unbracketed no client can resolve `src="cid:…"` under RFC 2392), and the
staged-upload id now derives from the same `deterministicGUID` as every other deterministic id in the
module (inventory `:1620`) rather than a second convention that differed from .NET in both byte order
and seed rendering.

## 7. Configuration

`STORAGE_PROVIDER` (`fs`, fail-closed when unset), `STORAGE_FS_ROOT`,
`STORAGE_FS_ALLOW_INSECURE_ROOT` (development only); `COMMUNICATIONS_RETENTION_DAYS`
(default 365), `COMMUNICATIONS_RETENTION_BATCH_SIZE`, `COMMUNICATIONS_RETENTION_POLL`;
`COMMUNICATIONS_ATTACHMENT_MAX_BYTES` (default 10 MiB),
`COMMUNICATIONS_UPLOAD_EXPIRY` (default 24 h); `COMMUNICATIONS_AI_ENABLED`,
`COMMUNICATIONS_AI_PROVIDER` (default `openai`), `COMMUNICATIONS_AI_MODEL`
(default `gpt-4o-mini`), `COMMUNICATIONS_AI_API_KEY`; `WORKERS_IN_PROCESS`.
Per-channel SMTP credentials are sealed with `internal/secrets` under their own purpose.
Every new value joins the existing redaction mirror.

## 8. Testing

Ported-test census in .NET test methods, a `[Theory]` counting once — the metric settled
in sub-project 4. 78 facts exist; six files are dropped wholesale by scope (Mailgun
inbound 5, Mailgun integration 5, Mailgun delivery 4, tenant rotation 4, tenant
isolation 2, attachment scanning 2 = 22), leaving ~56 before partial drops inside kept
files. The plan states the exact figure per file and the total; the inventory, not this
estimate, is the source.

Gated concurrency tests on the pattern the previous modules established — a gate that
polls `pg_stat_activity` for a genuinely blocked backend rather than sleeping — cover
the outbox claim race and the retention lease. The contract-coverage gate applies as it
does to the other three modules, and `pendingOperations` reaches empty.

## 9. Out of scope

Inbound mail in any form, attachment scanning, the Mailgun provider, and a Go
`contracts.ProductCatalog`. Each is additive later; none changes a contract this
sub-project ships.
