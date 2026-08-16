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
Enable or disable the module with `Modules__Communications__Enabled`.

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
`Modules__Customers__Enabled=true`.

Customer and Contact link values remain opaque domain values. Preserve them exactly;
do not derive meaning from them or use them as authorization credentials. The
in-process contract is responsible for authorized lookup and display of details.

## SMTP and Mailgun

The default Communications mailbox delivery uses the host's `Smtp:*` configuration:

```text
Smtp__Host=smtp.example.com
Smtp__Port=587
Smtp__Username=<smtp-user-from-secret-store>
Smtp__Password=<smtp-password-from-secret-store>
Smtp__UseSsl=false
Smtp__TimeoutSeconds=20
```

Mailgun is configured per email channel through the Communications API with provider,
domain, region, API key and inbound signing key fields. The API key and signing key
are protected at rest; they are never
placed in frontend code, browser storage, URLs, logs or deployment environment
files. Mailgun credentials are not a service-to-service integration.

SMTP credentials must be kept in a secret store. SMTP is for outbound mail;
inbound mail synchronization and IMAP support are not implemented.

### Mailgun Routes inbound

Configure a Mailgun Route with `forward("https://your-host/api/v1/communications/inbound/mailgun/{channelId}")`
and stop processing. The endpoint is anonymous and antiforgery-exempt, but accepts
only a valid Mailgun HMAC signature using that channel's inbound signing key. The
route must send Mailgun Routes form fields (`timestamp`, `token`, `signature`,
`sender`, `from`, `recipient`, `subject`, `body-plain`, `body-html`, optional
`body-mime`, `message-headers`, and multipart `attachment-*` files). This endpoint
does not accept Mailgun event-webhook JSON.

Signing keys are configured and rotated with the channel credentials API. During
rotation, update Mailgun and the channel together; an old key is not retained.
Successful, durably queued deliveries return `200`. Invalid signatures, stale or
future timestamps, malformed requests, replays, and permanent size-limit failures
return `406` so Mailgun does not retry. Temporary database/object-storage failures
return a non-2xx response and are retryable.

Raw EML is stored outside `wwwroot` under generated opaque object keys and is linked
to the normalized message only after processing. HTML is sanitized before it is
persisted: scripts, active content, forms, event handlers, remote images and unsafe
schemes are blocked; CID images are not exposed as downloadable content. Attachments
are stored with generated keys, `pending` scan status, hashes, size/type metadata,
and are not downloadable until a later scanning/download phase explicitly enables
them. Raw MIME, terminal inbound jobs/receipts and attachments follow the configured
communications retention period; failed terminal jobs without messages are included
while non-terminal work is retained. Retention queues each raw key once before
deleting terminal metadata, and cleanup retries object deletion through durable
cleanup records. Staged object reservations have a bounded expiry so a process crash
after the object write cannot strand the object.

Default inbound limits are 25 MiB per request/raw MIME, 10 MiB per attachment and
aggregate attachment bytes, 20 attachments, 8 MiB per text/HTML body, 100 form keys,
and a five-minute timestamp past/future skew. These can be changed under
`Communications:Inbound`; keep them aligned with Mailgun and deployment limits.

Production warning: the shared ASP.NET Data Protection key ring must itself be
protected with external key-ring encryption (certificate, KMS, or equivalent) so
Mailgun API/signing credentials remain protected at rest.

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
limited to 10 MiB and staged uploads expire after 24 hours. ClamAV scanning uses bounded
timeouts, size limits, and retries when configured; without a scanner, attachments remain
pending.

Conversation detail exposes `replyRecipients` (`canReply`, `canReplyAll`, `replyTo`,
and validated `replyAllCc`). Reply-all uses only the latest inbound message's CC,
excluding the channel mailbox and latest sender.

## Retention and module security

Use a conservative 12-month retention period unless the operator's policy requires
less. Configure `Communications__Retention__Days`, `BatchSize` and `PollMinutes`
as needed. Access controls and audit requirements must remain in force while data
is deleted.
