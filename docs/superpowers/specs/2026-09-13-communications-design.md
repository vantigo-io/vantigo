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
`direction` column even though `inbound` becomes unreachable.

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
