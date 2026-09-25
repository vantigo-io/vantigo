# Communications module deployment and integration

Communications is a module inside the single Vantigo binary, not a separately
deployed service. It runs in the same process as Identity, Customers, Products and
Energy, and owns the `communications` schema in the shared PostgreSQL database. The
production artifact is the single `ghcr.io/vantigo-io/vantigo` image.

## Running

The image is one binary with a dispatch table; the command is the first argument.
The commands that matter here are:

```bash
docker run --rm  ghcr.io/vantigo-io/vantigo migrate   # apply migrations and exit
docker run -d    ghcr.io/vantigo-io/vantigo api       # migrate, then serve (+ workers)
docker run -d    ghcr.io/vantigo-io/vantigo worker    # background workers only
```

`migrate` applies all migrations and exits. **`api` applies pending migrations under
the advisory lock and then serves**, running every enabled module's background
workers in-process when `WORKERS_IN_PROCESS=1` (the default). `server` serves only
and never migrates or runs workers — it is what a fleet of stateless replicas runs.
`seed` is development-only and currently does nothing.

There is no `reset-communications` command. The communications schema has a clean
initial migration lineage, there is no production communications data to preserve,
and migrations are forward-only.

## Enabling the module

`MODULES` is a **positive allowlist**, not a per-module boolean: a comma-separated
list of the business modules this deployment serves, parsed once at startup.

```text
MODULES=customers,products,energy,communications
```

That is also the default when `MODULES` is unset. Identity is always mounted and is
never listed. A name the binary does not know fails startup, naming the name and the
known set.

**Communications requires Customers.** Enabling it without `customers` fails startup
naming both. A disabled module contributes no route, no permission and no contract
path, and its paths answer the `/api` catch-all 404 — but every schema is migrated
regardless of what `MODULES` enables, so enabling the module later needs no
migration.

## In-process customer integration

Communications reads customer data through the in-process
`contracts.CustomerDirectory` interface (`apps/server/internal/contracts`). There is
no Customers service URL, no service-to-service API key and no `X-Vantigo-Api-Key`
variable. The customer directory is the one sanctioned cross-module read: modules
never import each other and never query another module's schema.

Customer and contact link values are opaque domain values. Preserve them exactly; do
not derive meaning from them or use them as authorization credentials.

The one write in the other direction is a customer merge: communications declares a
`contracts.CustomerReferenceHolder` ([module boundaries rule 8](module-boundaries.md#the-rules)),
and when the customers module merges two customers it re-points, inside the merge's
own transaction, every conversation's `customer_id` and `suggested_customer_id` from
the absorbed customer to the survivor, and the candidate list — where a conversation
that already lists the survivor keeps it once. See
[Merging duplicates](customers.md#merging-duplicates).

It also hands over and takes out what it holds about a private person —
`contracts.CustomerPersonalData` ([module boundaries rule 9](module-boundaries.md#the-rules)).
A person's export carries every conversation about them, with each message's
direction, date and body — the text body and the HTML body, each when the message has
it, so an HTML-only message is not left empty — and each attachment's name
(`modules.communications`). It leaves out the addressing metadata — each participant's
address and display name, each delivery's recipient address, a message's channel
headers — although the erase below deletes all of it with the conversations. The
suppressions, the opt-out addresses, are neither exported nor erased: honouring an
opt-out needs the address, so it is kept on purpose.
Their anonymisation deletes those conversations inside the customers module's
transaction, through the retention worker's own deletes: every message and the rows
under it in the order `message_events`' RESTRICT allows, each attachment, raw payload
and staged upload's object queued on the cleanup ledger before the rows naming it go —
the cleanup worker deletes the objects after the transaction commits — and a message
still waiting in the outbox with its job. A conversation that only suggests or lists
the person keeps its own customer and loses the suggestion, its reasoning and the
candidate row. The anonymisation reports `communications.objects` as the number of
object keys it queued — attachments, raw payloads and staged uploads, a key already on
the ledger counted too — not as a number of attachment rows. See
[Personal data and anonymisation](customers.md#personal-data-and-anonymisation).

What the erase does not cover, on the record: this module takes no customer lock, so a
message written into one of the person's conversations while the erase runs goes with
the conversation by cascade rather than through the ledger — or trips `message_events`'
RESTRICT, which rolls the customer back and the worker retries next cycle; nothing stops
a conversation being linked to the archived "Anonymised person" afterwards;
`communications.suppressions` can still hold the person's address; a conversation not
linked to the customer (no customer, or another one) keeps the person's participant
address, and the export does not list the addresses the person wrote from or was
written to; and a customer's
`customer.peppol_lookup` entries keep their `smpHost`, derived from the participant id.

## SMTP

**Per-channel SMTP credentials are configured through the Communications API, not
through the environment.** A channel's password is sealed with AES-256-GCM under a
dedicated purpose by `internal/secrets`, whose key is derived from `APP_SECRET` with
HKDF-SHA256. It is never placed in frontend code, browser storage, URLs, logs or
deployment environment files, and it is never returned by the API.

Two consequences follow from the key derivation: every replica must share the same
`APP_SECRET`, and changing `APP_SECRET` makes every stored channel credential
unrecoverable — they must be re-entered.

The `SMTP_*` environment variables are a **different thing**: they configure
identity's own application mail (invitations and password resets), not the mailboxes
conversations are sent through. See
[identity and authentication](customers-authentication.md).

Channels are SMTP-only. The API refuses to create or update a channel with any other
provider, so no channel of another kind is reachable through ordinary use.

**Communications is outbound-only.** There is no inbound mail path of any kind: no
webhook, no IMAP, no inbound synchronisation, and therefore nothing that writes an
inbound message. See "The outbound-only consequence" below for what that means for
replies, reply-all and the AI draft.

### Delivery semantics

Outbound email is **at-least-once**. The outbox worker claims a job with a lease,
performs the external send, and only then commits completion. If the process dies
between provider acceptance and the completion commit, the lease expires and another
worker resends the message — a duplicate email is possible after a crash or
deployment in exactly that window, and that trade-off is deliberate: the alternative
(at-most-once) silently loses mail.

Three mechanisms keep this honest:

- Every send uses a **deterministic `Message-Id`** derived from the message's id, so
  a resent message carries the same identity and receiving mail systems can collapse
  duplicates.
- Immediately before the external call, the job is stamped with
  `delivery_attempted_at` in its own committed write. A job re-claimed with that
  stamp set may already have been delivered; the resend still happens, but it is
  logged as a possible duplicate and counted on the
  `communications.outbox.possible_duplicate_sends` metric (meter
  `Vantigo.Communications`) — alert on it rather than discovering duplicates from
  customer reports.
- There is deliberately **no retry around the send call itself**: an ambiguous
  timeout may already have delivered, and the outbox owns retries.

A failed send is rescheduled with an exponential backoff of
`min(3600, 2^min(attempts, 10))` seconds, and `attempts` is incremented when the job
is claimed rather than when it fails. Two consequences of that expression are worth
stating because the arithmetic is easy to read wrongly:

- **The 3600 s cap is unreachable.** The exponent is clamped at 10, so the term never
  exceeds 1024 s and the `min` never binds.
- **With the default `max_attempts = 8`, the largest backoff a live job ever waits is
  128 s.** The sequence is 2, 4, 8, 16, 32, 64, 128 s, and the eighth attempt is
  terminal — so 256, 512 and 1024 s are never scheduled either.

The advisory lease guards the **retention worker only**. Outbox delivery and
attachment cleanup take no advisory lock: each claims work with a conditional update
whose `WHERE` re-asserts the predicate its candidate query used, so a claim another
worker already won matches nothing. Every replica runs those two workers every cycle,
by design. Adding an advisory lock to either would be a defect, not a hardening.

### Background workers

The module contributes the only three background workers in the application: outbox
delivery, retention, and attachment cleanup. `worker` mode always runs them, `server`
mode never does, and `api` mode runs them when `WORKERS_IN_PROCESS=1` (the default).
A deployment that scales the API horizontally should set `WORKERS_IN_PROCESS=0` on
the API replicas and run one dedicated `worker` container, so extra HTTP replicas do
not multiply the pollers.

### Metrics

Four counters on an OpenTelemetry meter named `Vantigo.Communications`, all of them
the outbox's: `communications.outbox.possible_duplicate_sends`, `.jobs_completed`,
`.jobs_retried` and `.jobs_failed`. **Alert on the last one: every increment is
undelivered customer email.** There are deliberately no others — no retention,
cleanup, storage, SMTP or AI metrics, and no histograms.

## Retention, cleanup and staged objects

Terminal jobs, their receipts and their attachments follow the configured retention
period; failed terminal jobs without messages are included, while non-terminal work
is retained. Retention queues each object key once, through a durable cleanup record,
before deleting the metadata that identifies its owner — it never calls the object
store itself. The attachment cleanup worker drains those records and retries object
deletion. Staged object reservations have a bounded expiry, so a process crash after
the object write cannot strand the object.

| Variable | Purpose | Default |
| --- | --- | --- |
| `COMMUNICATIONS_RETENTION_DAYS` | How old terminal history must be before deletion (1–36500) | `365` |
| `COMMUNICATIONS_RETENTION_BATCH_SIZE` | Messages one batch may delete (1–1000) | `100` |
| `COMMUNICATIONS_RETENTION_POLL` | How often the retention worker runs a cycle | `1h` |
| `COMMUNICATIONS_ATTACHMENT_MAX_BYTES` | Per-file staging limit (1 B – 50 MiB) | `10485760` (10 MiB) |
| `COMMUNICATIONS_UPLOAD_EXPIRY` | How long a staged attachment stays valid | `24h` |

A value outside the accepted range fails startup rather than being silently clamped,
so a typo is a boot error instead of a retention window nobody asked for. Use a
conservative twelve-month retention unless policy requires less; access controls and
audit requirements remain in force while data is deleted.

## Attachments

Stage authenticated multipart attachments with
`POST /api/v1/communications/conversations/{id}/attachments` using a `file`, optional
`contentId`/`isInline`, and an `Idempotency-Key`. Include returned upload IDs as
`attachmentIds` in the reply request; `replyMode` is `reply` by default or
`reply_all`. Uploads are sendable only once `scanStatus=clean`; while pending, reply
returns 409 `attachments_not_ready`.

Poll staged upload state with
`GET /api/v1/communications/conversations/{conversationId}/attachments/{attachmentId}`.
The uploader must hold ConversationsView and the upload must belong to the supplied
conversation. Other users, other conversations, expired uploads and unknown IDs
return 404 without storage details.

`GET /api/v1/communications/attachments/{id}/download` requires ConversationsView and
streams only clean attachments through the application. It never redirects to a
provider URL and never exposes a storage key; the `downloadPath` field in
conversation DTOs is this same-origin endpoint. Attachments are stored under the
`communications` storage scope — see [object storage](storage.md), which is
**filesystem-only** in this release and fails closed at each operation when
`STORAGE_PROVIDER` is unset.

**There is no attachment scanner.** Nothing transitions an upload from `pending` to
`clean`, so new uploads are created `clean` on the reasoning that there is nothing to
scan. The `scanStatus` and `ready` fields, and every gate that reads them, are kept
exactly as they are: the frontend is unchanged and a scanner can be added later
without a contract change. Do not read a `clean` status as evidence that a file was
inspected.

## The outbound-only consequence

Reply-all resolves its recipients from the latest **inbound** message's CC, and the
reply path resolves its recipients from that message's participants. With no inbound
path, no such message exists, so these are permanently in their negative state rather
than occasionally empty:

- A conversation created through `POST /conversations` cannot be replied to: reply
  answers 422 `recipients_missing`.
- `canReplyAll` is always false and `replyAllCc` is always empty.
- The AI draft builds its context from inbound messages, so it drafts against an
  empty context and records `product_data=false`.

Two further consequences are worth knowing when reading the code: attachments can be
staged but never sent, and suppression is enforced only by the outbox worker's own
re-check, because reply's suppression check sits behind the recipient resolution that
always fails first.

The endpoints are complete and conform to the contract, so the frontend keeps working
and an inbound provider is purely additive.

## AI draft and customer suggestion

Both AI operations answer 503 `ai_unavailable` unless all three conditions hold:
`COMMUNICATIONS_AI_ENABLED=1`, `COMMUNICATIONS_AI_PROVIDER` is `openai` (the
default), and `COMMUNICATIONS_AI_API_KEY` is set. `COMMUNICATIONS_AI_MODEL` defaults
to `gpt-4o-mini`. A half-set configuration is the documented unavailable state, not a
startup error: no chat client, network client or provider dependency is constructed
at all in that state, and any other provider name leaves the feature unavailable
rather than refusing to start, so a second provider stays additive.
