---
title: "Meta module"
description: "Meta as the connection to Facebook Messenger through a Facebook Page the business owns — the module and why it requires Communications; the schema; the settings and the sealed secrets; the Owner policy and the rate limits; the webhook (the GET handshake, the signed POST, its status codes, store then answer); the inbound queue and the splitting worker (the cursor, the advisory lock, the dedup keys, the fetch from Meta's CDN, the read of echoes, postbacks and quick replies); Send, the Human Agent tag and the Graph error map; the Pages (add, remove, check, the profile cache); the capabilities; retention and personal data; the workers; the testing seam; and where it departs from the design."
sources:
  - apps/server/internal/meta
  - apps/meta/frontend
  - apps/host/frontend/src/routes/workspace/meta.tsx
  - openapi/meta.yaml
  - apps/server/internal/db/migrations/00137_meta_baseline.sql
---

The Meta module is the connection between Vantigo and **Facebook Messenger**. It fills
the `ChannelMessenger` slot of Communications under the key `meta`
([The ChannelMessenger slot](/en/reference/communications/#the-channelmessenger-slot)), so
that a Messenger channel there is a Facebook Page the business owns. It holds the Meta
app's id and secret, the Pages' access tokens, the webhook Meta posts its events to, the
queue of what people sent, and the calls to Meta's Graph API. The conversations, the
outbox and the reply window are Communications'; this page is what Meta's side does. The
operator's procedure is [Meta](/en/admin/meta/).

Meta is a module **without an app**, like Entra and Google: no tile and no app key. Its
page is **Integrations** › **Meta** at `/workspace/meta`, an Owner's alone, shown while the
module is on. It adds no Assistant route prefix.

## The module

- **`MODULES`.** `meta` requires `communications`; `MODULES` refuses it without
  (`meta requires communications`). It imports nothing; no SQL of it crosses a schema.
- **The contract** is `openapi/meta.yaml`, mounted under `/api/v1/meta/`. The API
  reference is generated from it.
- **The schema** is `meta`, created by migration `00137_meta_baseline.sql` whether or not
  the module is enabled.

| Operation | Method and path | Access |
| --- | --- | --- |
| `getMetaSettings` | `GET /api/v1/meta/settings` | Owner |
| `putMetaSettings` | `PUT /api/v1/meta/settings` | Owner |
| `postMetaSettingsVerifyToken` | `POST /api/v1/meta/settings/verify-token` | Owner |
| `postMetaPages` | `POST /api/v1/meta/pages` | Owner |
| `deleteMetaPagesByPageId` | `DELETE /api/v1/meta/pages/{pageId}` | Owner |
| `getMetaWebhook` | `GET /api/v1/meta/webhook` | anonymous (the public host) |
| `postMetaWebhook` | `POST /api/v1/meta/webhook` | anonymous (the public host) |

The module has no permission of its own. The five Owner operations are checked against the
`Owner+OwnerManagement` policy (an Owner, signed in with a second factor while
`OWNERS_REQUIRE_MFA` is on). A refusal other than a field error is an RFC 9457 problem
whose `code` a client keys off, never the words.

### Rate limits

Per client address, a minute, before access is checked:

| Policy | Limit | Operations |
| --- | --- | --- |
| `meta-owner` | 30 | the five Owner operations, one bucket between them |
| `meta-webhook` | 1200 | `POST /webhook`; a busy Page posts several events a second |
| `meta-webhook-verify` | 60 | `GET /webhook`; a guess at the verify token is what it is for |

A limited request is answered 429 with `Retry-After`.

## Schema

| Table | Holds |
| --- | --- |
| `meta.settings` | The installation's one row (`id = 1`, seeded by the migration): `app_id`, `app_secret_sealed`, `verify_token` (generated), `human_agent_granted`, and the status line: `last_webhook_at`, `last_rejected_at`, `last_graph_error_code`, `last_graph_error`, `last_graph_error_at`, with `updated_at` and `updated_by_user_id`. |
| `meta.pages` | A connected Page: `page_id` (digits), `name`, `token_sealed`, `state` (`active` or `token_rejected`), `last_error`, `added_at`, `added_by_user_id`, `checked_at`. |
| `meta.webhook_events` | The raw queue: each post whose signature held, as `payload` (jsonb), with `state` (`pending`, `split` or `failed`), `attempts`, `next_attempt_at` (the lease and the backoff), `split_at` and `last_error`. A body Postgres refuses as jsonb has no `payload`: its bytes are in `raw`, with `state = 'failed'` and `last_error = 'unstorable_json'`. |
| `meta.pending_files` | The object-store keys of attachments written and not yet named by an inbound row: `storage_key` and `created_at`. See [Splitting](#splitting). |
| `meta.inbound` | The provider's inbox Communications reads: one row per message, postback, delivery or read event. See [The inbound queue](#the-inbound-queue). |
| `meta.profiles` | A participant's cached name by (`page_id`, `psid`), with `fetched_at`. |

### Settings and sealed secrets

- **App ID.** Digits, 1 to 40; anything else is a 400 on `appId`.
- **App secret.** At most 200 characters, sealed under the purpose `meta/app-secret` and
  never returned: the settings answer carries `appSecretSet` alone. An empty or absent
  `appSecret` on a save keeps the stored one.
- **Verify token.** 32 random bytes as 64 hex characters, generated by the migration and
  replaced by `POST /settings/verify-token`. The settings answer carries it, since an Owner
  pastes it into Meta's portal.
- **Page tokens** are sealed under `meta/page-token` and never returned.
- **Human Agent** (`humanAgentGranted`) is a plain flag: it records that Meta has granted
  the app the permission, which is applied for with Meta.
- **A save needs a public host.** `PUT /settings` answers 422 `public_host_required` while
  `PUBLIC_BASE_URL` is empty. The answer's `webhookUrl` is `PUBLIC_BASE_URL` followed by
  `/api/v1/meta/webhook`, and `publicHostConfigured` says whether it is known.
- The provider is **available** (`Status`) once the app ID and secret are both stored, else
  `not_configured`. `Status` reads the module's own row and never the network. A Page whose
  token Meta refused does not make the provider unavailable: that Page's endpoint is.

The status line is `status` in the settings answer: `lastWebhookAt` (a post with a valid
signature), `lastRejectedAt` (a post without), and the last refusal of the Graph API as
`lastGraphErrorCode` (`190`, `10/2018278`, or `human_agent_not_permitted`), its
`lastGraphErrorMessage` (cut to 200 characters) and `lastGraphErrorAt`. The first two are
written **at most once every 30 seconds**.

## The webhook

Both operations are on the public host's list, exactly these paths
([The public host](/en/admin/payments/#the-public-host)).

### The handshake

`GET /api/v1/meta/webhook?hub.mode=subscribe&hub.verify_token=…&hub.challenge=…`

- **200** with `hub.challenge` as `text/plain` when `hub.mode` is `subscribe` and
  `hub.verify_token` is the stored verify token.
- **403** otherwise, including while no token is stored.

Both tokens are hashed with SHA-256 before they are compared, and the hashes are compared
in constant time, so the comparison takes the same time whatever the lengths.

### The post

`POST /api/v1/meta/webhook` is a hand-written handler behind an operation whose body the
contract declares as **raw bytes** (`application/octet-stream`, binary), which the strict
wrapper hands on undecoded whatever content type Meta sends. The signature is over the bytes as Meta sent them, and a
body decoded and encoded again is other bytes, so the handler reads the raw request. It
reads up to **1 MiB** (the router's cap, named in the operation) and then:

1. A body over the cap is answered **413**; a body that cannot be read, **400**.
2. The settings are read and the app secret opened, within a 2-second budget. A database
   that fails, or **no app secret stored**, is **503**; Meta retries a 503.
3. `X-Hub-Signature-256` must be `sha256=` and the hex **HMAC-SHA256 of the body under the
   app secret**, compared in constant time. If not, **401**, and `last_rejected_at` is
   noted. Neither the body, the signature nor the secret is logged, and the warning is
   written at most **once a minute per client address**; the refusals held back are
   counted and the count is in the next warning.
4. A body that is not valid JSON is **400**. Nothing else is parsed.
5. The body is stored in `meta.webhook_events` (`pending`), `last_webhook_at` is noted, and
   Meta is answered **200**. A failing insert is **503**, except when Postgres refuses the
   body as jsonb (SQLSTATE `22P05` or `22021`, as for an escaped `\u0000` in a string):
   Meta would retry that for ever, so the bytes are kept in `raw` in a row that is born
   `failed` with `last_error` `unstorable_json`, a warning without the body is logged, and
   the answer is **200**. The splitting worker never claims such a row; retention deletes
   it with the other raw events.

Nothing is parsed for routing before the answer: **store, then 200**. What the body
means is the splitting worker's work, so a payload the module cannot read costs one event
and never the webhook.

## The inbound queue

`meta.inbound` is what Communications' sync reads. Its row `id` is a `bigint` identity,
the **cursor**: Communications reads `Pending(page, after, limit)` for the rows after its
cursor, oldest first (at most 200), and `Ack(page, upTo)` acknowledges up to and including
one, idempotently. Never a timestamp: webhooks arrive out of order.

| Column | |
| --- | --- |
| `page_id`, `participant` | The Page and the person's PSID |
| `kind` | `message`, `delivered` or `read` |
| `dedup_key` | Unique with `page_id` |
| `occurred_at` | The event's own time, else the entry's, else when it was split |
| `payload` | The normalised event: `mid`, `text`, `answer`, `echo`, `watermark`, `attachments` |
| `storage_keys` | An **array**, one object-store key per attachment, in the order of the payload's attachments |
| `acked_at` | When Communications acknowledged it |

`Notified(since)` answers the Pages with unacknowledged rows created after `since`.
`OpenAttachment(page, ref)` opens one attachment by `ref`, `<inbound id>:<index>`, from
the object store; a ref that names nothing is refused.

### Splitting

The worker `meta-split` runs every second. A cycle claims due events **one at a time**
(up to 20 a cycle), each under a 60-second lease, and for each:

1. **Plans** the rows. Only `object: page` payloads count; an entry for a Page that is not
   connected is dropped. Each messaging item becomes at most one row, by its kind:

   | Item | Row | Dedup key |
   | --- | --- | --- |
   | A message with text or files | `message` | `m:<mid>` |
   | A postback | `message` (its title as text) | `p:<postback mid>`, or a hash of Page, PSID, timestamp and payload when it has none |
   | A delivery watermark | `delivered` | `d:<PSID>:<watermark>` |
   | A read watermark | `read` | `r:<PSID>:<watermark>` |

   Duplicates (Meta delivers at least once) collide on (`page_id`, `dedup_key`) and are
   ignored. An item with no id, no content or no participant becomes no row.
2. **Fetches the files** a message carries (`image`, `audio`, `video` and `file`) **from
   Meta's CDN at once**, because the addresses expire, through the guarded client to
   `*.fbcdn.net` and `*.fbsbx.com`. A file over 25 MB, or one that cannot be fetched, defers
   the event; from the fifth attempt the file is left out of the row, and a message of
   nothing but files none of which could be kept is skipped. The files are written to the
   object store under the scope `meta-inbound` **before** any transaction: no provider call
   or storage write happens inside one. Before each fetch the event's lease is **extended
   to the fetch's budget plus 60 seconds**, so another replica never takes an event that
   is only slow and its attempts are not burned. A type the CDN names is stored as given
   only if it is an image, video or audio type of the common kinds, a PDF, a common office
   document, `text/plain`, `text/csv` or `application/zip` (parameters such as `charset`
   are ignored); anything else, `text/html` and `image/svg+xml` included, is stored as
   `application/octet-stream`. The key is **staged** in `meta.pending_files` before each
   write.
3. **Stores** the rows and marks the event split, in one transaction that first takes an
   **advisory lock**. The ids are handed out at insert, and without the lock a transaction
   that took id 7 could commit after one that took id 8, so a reader at cursor 8 would skip
   7 for good. Holding the lock from the first insert to the commit makes ids commit in
   order. The Page is read `FOR SHARE` in the same transaction, so a split racing Remove
   Page inserts nothing for the removed Page and deletes the files it wrote. The
   transaction clears the staging rows of the keys its rows name. A split that crashes or
   rolls back leaves its keys staged; [retention](#retention-and-personal-data) deletes
   those objects.

A failed attempt is deferred with a growing wait (5 s, 30 s, 2 min, 10 min, 30 min); a
payload that cannot be read at all is marked `failed`. Nothing a person wrote is stored
in `last_error`.

### Echoes, postbacks and quick replies

- **An echo** is a message the Page itself sent, with `is_echo` set; its participant is the
  recipient. Communications ignores an echo of its own sends (it knows them by the message
  ids in `messenger_parts`) and imports a **foreign** one, such as a reply typed in Meta's
  own inbox, as a message *Sent from Meta*.
- **A postback** whose payload is one Vantigo wrote is an **answer** to an option
  (`answer`: the message and the choice). One that is not is read as text.
- **A quick reply** with a payload of Vantigo's is an answer likewise. The **e-mail quick
  reply** (`user_email`) carries the address as its text with a payload that is not
  Vantigo's, so it is recognised by that and by the text being an address (one `@`, no
  space); this is **to be confirmed against a real Page**. Any other quick reply is read as
  text.

## Send

`Send(address, part)` sends one part of a message from the Page `address` to
`part.Participant` through the Graph API's `POST /{page}/messages`, with the Page's token
as a bearer header (never in a URL).

- **Refused before a call.** A provider not configured, an unknown Page, or a Page whose
  state is `token_rejected` answers *unavailable* (the outbox holds the job). A part with a
  card is refused (`Capabilities.MaxCards` is 0 until delivery C); so are a part with no
  recipient, nothing to send, text over 2000 characters, and an empty or over-25-MB file.
- **Text** goes as JSON; **a file** as `multipart/form-data` with the `filedata` part and
  `is_reusable` false. The attachment type is `image`, `audio` or `video` by the content
  type, else `file`.
- **Quick replies** are the part's answer options (title at most 20 characters, at most 13
  in all, each with Vantigo's payload) and, with `ShareEmail`, a `user_email` request.
- **`messaging_type`** is `RESPONSE`, or `MESSAGE_TAG` with `tag: HUMAN_AGENT` when the part
  is sent as a Human Agent message (outside 24 hours, within 7 days).
- **The answer** must carry a `message_id`, which Communications keeps as the platform
  message id.

### The error map

A refusal from Meta (HTTP status, `code`, `error_subcode`) becomes the outbox's outcome:

| Meta | Outcome |
| --- | --- |
| A network failure; HTTP 5xx or 429; codes 1, 2, 4, 17, 32, 613 | **Temporary**: the outbox retries |
| 190 | **Rejected**, terminal for the message; the Page becomes `token_rejected` |
| 551 | `recipient_unreachable` |
| 10 with subcode 2018278 | `reply_window_closed` |
| 10 or 200 on a tagged send (other than the above) | `human_agent_not_permitted` |
| Anything else | **Rejected**, with Meta's message |

Every refusal that is not temporary is kept as the status line's last Graph error (the
code as `190` or `10/2018278`; `human_agent_not_permitted` for the tagged refusal). The
recipient and the text are never logged or kept. Later sends to a Page set to
`token_rejected` are held as *unavailable*.

## Pages

- **Add** (`POST /pages`, body `accessToken`, at most 1024 characters, and an optional
  `pageId`, digits). 400 on `accessToken` when empty and on a `pageId` that is not digits.
  422 `not_configured` while the app is not configured. Then
  `GET /me/accounts?fields=id,name,tasks,access_token&limit=100` with the token (one page
  of 100; `paging.next` is not followed), which a user's or a system user's token answers
  with the Pages it manages. The Pages whose id is digits, that carry an `access_token` and
  whose `tasks` include `MESSAGING` are kept (only `pageId`, when given). None kept is 422
  `page_token_insufficient`; several kept and no `pageId` is 422 `page_choice_required`
  with `choices` (`{id, name}` each); one is used. That Page's own `access_token`, never
  the pasted one, is what is subscribed with and sealed. When Meta refuses the listing
  with a refusal that is neither temporary nor error 190, the token is taken for a Page's
  own: `GET /me?fields=id,name` reads the Page (`tasks` is not read; the id must be digits
  and equal `pageId` when given), and that the subscription below succeeds is the proof
  that the token may act for it. Error 190 and any other non-temporary refusal of these
  reads or of the subscription is 422 `page_token_insufficient`; a call that could not be
  made or a temporary refusal is **503** `meta_unreachable`. Then
  `POST /{page}/subscribed_apps` with `messages`, `messaging_postbacks`, `message_reads`,
  `message_deliveries` and `message_echoes`. Only then is the token sealed and the Page
  stored: **201** with the Page. The same Page added again replaces its token and clears a
  rejection.
- **Remove** (`DELETE /pages/{pageId}`, **204**; **404** `page_not_found`). The app's
  subscription is withdrawn on a best-effort basis; the Page's inbox rows and cached
  names, the Page and then the files of its rows are deleted.
- **`Endpoints`** lists the Pages from the database, each with its state (available, or the
  reason `token_rejected`). **`Check`** is the live read: `GET /me` refreshes the name and
  sets the Page `active`, or, on 190, sets it `token_rejected`; a Page that is not
  connected is unknown.
- **`Profile`** is a participant's display name, from `GET /{psid}` with `name`,
  `first_name` and `last_name`, cached in `meta.profiles` for **30 days**. A person Meta
  gives no name for yields an empty profile, not cached. A Page set to `token_rejected`
  makes it *unavailable*.

## Capabilities

What a message on Messenger may carry (`Capabilities`, which Communications checks a reply
against and which makes no network call):

| Limit | Value |
| --- | --- |
| Text | 2000 characters |
| Answer options | 13, each title at most 20 characters |
| E-mail request | allowed |
| Attachment | 25 MB |
| Cards | none (0) until delivery C |
| Reply window | 24 hours |
| Human Agent window | 7 days, **only while the Human Agent setting is on**; a settings row that cannot be read gives the 24-hour window |

## Retention and personal data

The module holds PSIDs, names, message text and files, and nothing for longer than:

| What | Deleted after |
| --- | --- |
| `meta.webhook_events` rows | 7 days from receipt |
| Acknowledged `meta.inbound` rows, with their files | 7 days from acknowledgement |
| Any `meta.inbound` row, acknowledged or not, with its files | 30 days from creation |
| `meta.profiles` rows | 30 days from fetch |
| Staged files no inbound row names (a split that crashed or rolled back) | 1 day from staging |

The worker `meta-retention` runs hourly. Rows are deleted in batches of 200, files first:
a row goes only once every one of its files is gone, so a failure leaves it for the next
pass. Acknowledging deletes nothing: the provider keeps its copy, rows and files, for the 7 days
after the acknowledgement, so a read that failed after it can be made again. A staged key
older than a day that no `meta.inbound.storage_keys` contains has its object deleted (a
missing one counts as deleted) and its staging row removed; one that a row does name just
loses the staging row.

The module knows **no customer ids**, so it implements no customer erasure: retention is
its only bound. Communications' copies are covered by its own erasure, and a participant
row there outlives a customer's erasure until Communications' orphan sweep.

## Workers

| Worker | Cycle | Does |
| --- | --- | --- |
| `meta-split` | 1 second | Splits stored webhook events into `meta.inbound` and fetches their files |
| `meta-retention` | 1 hour | Deletes what is past [its period](#retention-and-personal-data) |

Both run in the API process and in worker mode. `meta-split` claims events under a lease, so
a replica that dies lets go of its events by itself and any number of instances share the
work.

## Reaching Meta

The module reaches the network in one place, a guarded client that goes through
`internal/netguard` with no proxy and no redirect, to `graph.facebook.com` (version
`v21.0`) and to Meta's CDN (`*.fbcdn.net`, `*.fbsbx.com`). A call is bounded to 30 seconds,
and a file's fetch or upload to 3 minutes (Communications' longest per-part send), and every call carries an `appsecret_proof`, the hex HMAC-SHA256 of the access token under the app secret, so Meta can refuse a leaked token used without the secret.

## Testing

`Deps.HTTPTransport` is the test seam. The fake Meta, `internal/meta/metatest`, is an
`http.RoundTripper` standing in for `graph.facebook.com`: Pages with their tokens and
tasks, people who wrote to them, `/me`, `subscribed_apps`, `/messages` (JSON and
multipart) and `/{psid}`, bearer-token checks (an unknown token is Graph error 190), a
record of every call and message, and scripted failures. Only `_test.go` files may import
it.

## Where it departs from the design

The design is `docs/superpowers/specs/2026-11-05-communications-messenger-design.md`
(N9 to N12a).

- `meta.inbound.storage_keys` is an **array**, one key per attachment, not a single key.
- Rows **never acknowledged are deleted after 30 days**, so the 30-day bound on personal
  data holds in fact.
- Graph error **190 on a send** is terminal for that message, and later sends to the Page
  are held as *unavailable* until the Page is added again.
- Meta adds **no Assistant route prefix**: it is a module without an app, like Entra and
  Google, and its page is `/workspace/meta`.
- The last webhook's time is written **at most once every 30 seconds**.
- The `user_email` quick reply is recognised by its payload not being Vantigo's and its
  text being an address (to be confirmed against a real Page).
