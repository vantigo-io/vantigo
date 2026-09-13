# Communications module deployment and integration

Communications is a module loaded by `Vantigo.Host`, not a separately deployed
service. It runs in the same process as Identity, Customers and Products and uses
the shared PostgreSQL database's `communications` schema. The production artifact
is the single `ghcr.io/vantigo-io/vantigo` image.

## Running the host

The host commands are `api`, `migrate`, `seed`, and the explicitly confirmed
Development-only `reset-communications` safety command.

```bash
dotnet run --project apps/host/backend/Vantigo.Host --launch-profile migrate
dotnet run --project apps/host/backend/Vantigo.Host --launch-profile seed
dotnet run --project apps/host/backend/Vantigo.Host --launch-profile api
VANTIGO_CONFIRM_RESET_COMMUNICATIONS=true dotnet run --project apps/host/backend/Vantigo.Host -- reset-communications
```

`migrate` applies all enabled module migrations and exits. `seed` is
Development-only. `api` hosts the application and does not migrate or seed.
Enable or disable the module with `Modules__Communications__Enabled`. Disabling
it removes the module completely: no services, no outbox/retention/attachment
workers, no endpoints (its routes answer `404`), no permissions, and no
migrations or seeding.

Normal `migrate` does not reset or repair a retired Communications schema. There
is no production Communications data migration in this release. Development
users with a legacy local Communications schema must run the exact
`reset-communications` command above, with
`VANTIGO_CONFIRM_RESET_COMMUNICATIONS=true`; never run it in production. The
command first deletes only known Communications objects through the scoped
Communications object store and drops the schema only after those deletes
succeed, then applies the current migrations.

## In-process customer integration

Communications accesses customer data through the in-process
`Vantigo.Contracts.ICustomerDirectory` contract. There is no Customers service URL,
S2S API key or `X-Vantigo-Api-Key` environment variable. The host registers the
Customers module and its contract implementation when
`Modules__Customers__Enabled=true`, and refuses to start when Communications is
enabled without it.

Customer and Contact link values remain opaque domain values. Preserve them exactly;
do not derive meaning from them or use them as authorization credentials. The
in-process contract is responsible for authorized lookup and display of details.

## SMTP

Mailbox delivery uses the host's `Smtp:*` configuration:

```text
Smtp__Host=smtp.example.com
Smtp__Port=587
Smtp__Username=<smtp-user-from-secret-store>
Smtp__Password=<smtp-password-from-secret-store>
Smtp__UseSsl=false
Smtp__TimeoutSeconds=20
```

Per-channel SMTP credentials are configured through the Communications API and
are protected at rest; they are never placed in frontend code, browser storage,
URLs, logs or deployment environment files. Host-level SMTP credentials must be
kept in a secret store.

**Communications is outbound-only.** There is no inbound mail path of any kind:
no webhook, no IMAP, no inbound synchronisation, and therefore nothing that
writes an inbound message. See "The outbound-only consequence" below for what
that means for replies, reply-all and the AI draft.

### Delivery semantics

Outbound email is **at-least-once**. The outbox worker claims a job with a
lease, performs the external send, and only then commits completion. If the
process dies between provider acceptance and the completion commit, the lease
expires and another worker resends the message — a duplicate email is
possible after a crash or deployment in exactly that window, and that
trade-off is deliberate: the alternative (at-most-once) silently loses mail.

Three mechanisms keep this honest:

- Every send uses a **deterministic `Message-Id`** derived from the message's
  id, so a resent message carries the same identity and receiving mail systems
  can collapse duplicates.
- Immediately before the external call, the job is stamped with
  `DeliveryAttemptedAt` in its own committed write. A job re-claimed with
  that stamp set may already have been delivered; the resend still happens,
  but it is logged as a possible duplicate and counted on the
  `communications.outbox.possible_duplicate_sends` metric
  (meter `Vantigo.Communications`) — alert on it rather than discovering
  duplicates from customer reports.
- There is deliberately **no retry around the send call itself**: an ambiguous
  timeout may already have delivered, and the outbox owns retries.

A failed send is rescheduled with an exponential backoff of
`min(3600, 2^min(attempts, 10))` seconds, and `attempts` is incremented when the
job is claimed rather than when it fails. Two consequences of that expression are
worth stating because the arithmetic is easy to read wrongly:

- **The 3600 s cap is unreachable.** The exponent is clamped at 10, so the term
  never exceeds 1024 s and the `min` never binds.
- **With the default `max_attempts = 8`, the largest backoff a live job ever
  waits is 128 s.** The sequence is 2, 4, 8, 16, 32, 64, 128 s, and the eighth
  attempt is terminal — so 256, 512 and 1024 s are never scheduled either.

The advisory lease (`CommunicationsAdvisoryLease`) guards the **retention worker
only**. Outbox delivery and attachment cleanup take no advisory lock: each
claims work with a conditional update whose `WHERE` re-asserts the predicate its
candidate query used, so a claim another worker already won matches nothing.
Every replica runs those two workers every cycle, by design.

## Retention, cleanup and staged objects

Terminal jobs, their receipts and their attachments follow the configured
communications retention period; failed terminal jobs without messages are
included, while non-terminal work is retained. Retention queues each object key
once, through a durable cleanup record, before deleting the metadata that
identifies its owner — it never calls the object store itself. The attachment
cleanup worker drains those records and retries object deletion. Staged object
reservations have a bounded expiry, so a process crash after the object write
cannot strand the object.

The communications schema uses a clean initial migration lineage. There is no
production communications data to preserve; the deleted legacy outbound-only
migration must not be restored or replaced with a data migration.

## Attachments and reply-all

Stage authenticated multipart attachments with
`POST /api/v1/communications/conversations/{conversationId}/attachments` using a
`file`, optional `contentId`/`isInline`, and an `Idempotency-Key`. Include returned
upload IDs as `attachmentIds` in the reply request. `replyMode` is `reply` by default
or `reply_all`. Uploads are sendable only after `scanStatus=clean`; while pending,
reply returns 409 `attachments_not_ready`.

Poll staged upload state with
`GET /api/v1/communications/conversations/{conversationId}/attachments/{attachmentId}`.
The authenticated uploader must have ConversationsView and the upload must belong to
the supplied conversation. The cache-disabled `AttachmentUploadResponse` is
`{ id, fileName, contentType, sizeBytes, scanStatus, isInline, expiresAt, ready }`.
`scanStatus` is `pending`, `scanning`, `clean`, `quarantined`, or `failed`; `ready` is
true only for `clean`. Other users, other conversations, expired uploads, and unknown
IDs return 404 without storage details.

`GET /api/v1/communications/attachments/{id}/download` requires ConversationsView and
application-streams only clean attachments. The endpoint authorizes the attachment,
loads it through the scoped Communications object store, and returns an attachment
response with the stored content type and sanitized filename; it never redirects to a
provider URL or exposes a storage key. The `downloadPath` field in conversation DTOs is
this same-origin application endpoint, not a direct object-storage URL. See
[`docs/storage.md`](storage.md) for the provider-neutral storage contract. Files are
limited to 10 MiB and staged uploads expire after 24 hours.

**There is no attachment scanner.** Nothing transitions an upload from `pending`
to `clean`, so new uploads are created `clean` on the reasoning that there is
nothing to scan. The `scanStatus` and `ready` fields, and every gate that reads
them, are kept exactly as they are: the frontend is unchanged and a scanner can
be added later without a contract change. Do not read a `clean` status as
evidence that a file was inspected.

Conversation detail exposes `replyRecipients` (`canReply`, `canReplyAll`, `replyTo`,
and validated `replyAllCc`).

## The outbound-only consequence

Reply-all resolves its recipients from the latest **inbound** message's CC, and
the reply path resolves its recipients from that message's participants. With no
inbound path, no such message exists, so these are permanently in their negative
state rather than occasionally empty:

- A conversation created through `POST /conversations` cannot be replied to:
  reply answers 422 `recipients_missing`.
- `canReplyAll` is always false and `replyAllCc` is always empty.
- The AI draft builds its context from inbound messages, so it drafts against an
  empty context and records `product_data=false`.

Two further consequences are worth knowing when reading the code: attachments can
be staged but never sent, and suppression is enforced only by the outbox worker's
own re-check, because reply's suppression check sits behind the recipient
resolution that always fails first.

The endpoints are complete and conform to the contract, so the frontend keeps
working and an inbound provider is purely additive.

## Retention and module security

Use a conservative 12-month retention period unless the operator's policy requires
less. Configure `Communications__Retention__Days`, `BatchSize` and `PollMinutes`
as needed. Access controls and audit requirements must remain in force while data
is deleted.
