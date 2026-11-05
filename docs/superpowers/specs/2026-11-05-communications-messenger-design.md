# Communications — Messenger, answer options and product images (design)

**Revision 2, 2026-11-05**, after a critic review. §11 lists what changed.

This design is drawn from the research
[`2026-11-05-communications-messaging-channels.md`](../research/2026-11-05-communications-messaging-channels.md)
("R"). It does not reopen R's §3 choice. Decisions are numbered N*n*. The implementation
plan is [`2026-11-05-communications-messenger-plan.md`](../plans/2026-11-05-communications-messenger-plan.md).

Abbreviations:

- `srv/` stands for `apps/server/internal/`;
- `mig/` stands for `srv/db/migrations/`;
- `U/`, `A/` and `Ref/` stand for the user guide, the administration guide and the reference
  under `docs/src/content/docs/en/`. `U/` and `A/` also exist under `nb/`.

## 1. Purpose

Communications gets its first channel that is not e-mail: **Facebook Messenger**, through a
Facebook Page the business owns.

A message on that channel can carry more than text:

- **Answer options.** Yes / No, or options the agent writes. The customer taps one, and the
  answer comes back into the conversation.
- **A product.** The agent attaches a product, and the message shows a carousel of its images.
  Each image has a *Buy* choice, which comes back to the agent as a request to buy.

Products gets **images**. Each image carries where it may be used: in Communications, and in
marketing.

### The owner's choices (2026-11-05)

These are settled:

- **Messenger comes first.** WhatsApp is set aside, because few in Norway use it for business.
  Instagram comes later.
- **The business owns the Meta app** in this phase (R §3 B). A Meta app owned by Vantigo, with
  a central relay, comes later. Only the onboarding changes then.
- **Answer options:** Yes / No, plus options of the agent's own.
- **Product images carry usage flags:** whether an image may be shown in Communications, and
  whether it may be shown in marketing.
- **Buy is a choice that comes back to the agent.** It is not a checkout.

### Deliveries

| Delivery | What it brings | Depends on |
| --- | --- | --- |
| **0** | **The Meta checks** (§8), before B is built. Does an app owned by the business need App Review and Business Verification before the public can write to its Page? Does Meta keep a carousel image after fetching it? Run with a test app and a test Page that the owner creates | — |
| **A** | **Groundwork for a messaging channel in Communications.** Channel types beyond e-mail, with defaults per type. The `contracts.ChannelMessenger` slot. A messenger sync worker of its own. Components on messages, and answers. Threading by participant. The reply window. Sending in parts, idempotently. Chat rendering and a chat composer. All of it proven against a fake provider | — |
| **B** | **Messenger.** A new integration module, **Meta** (`meta`): the app settings under Integrations, the webhook with its signature, Pages, sending and receiving text and attachments, answer options, echoes, and read and delivery events | A, and 0's answers |
| **P** | **Product images in Products.** Upload with normalisation, order, a primary image, alt text and the usage flags. Shown on the product page, and read by other modules through `contracts.ProductCatalog` | — |
| **C** | **A product in a message.** *Attach product* in the composer. The carousel built from the product's images marked for Communications. The public media route Meta fetches images from. *Buy* coming back to the agent, with a link to a new order | A, B, P, and 0's answer on image caching |

A and P can start at once. Delivery 0 runs beside them. A is neutral of the channel, so it is
worth building whichever way 0 answers.

### Not in this phase

- WhatsApp, Instagram and SMS.
- A Meta app owned by Vantigo and its central relay.
- Facebook Login for connecting a Page.
- Checkout from the chat.
- Several products in one carousel.
- Bots and automatic replies.
- Answer options on e-mail.
- Images of a single variant.
- Reordering answer options by dragging.
- Product-list thumbnails.
- An inbox filter by channel type.
- Anything that reads the marketing flag. It is stored and edited only, because the owner asked
  for it.

§9 says what each of these waits for.

## 2. Decisions — delivery A, the groundwork in Communications

### N1 — A channel's type is `email` or `messenger`, with a default per type

**The type gate.** `channels.go:400` accepts `messenger` beside `email`. The address rule
depends on the type:

- an e-mail channel's address stays RFC 5322;
- a Messenger channel's address is the Page ID, `[0-9]{1,30}`.

**The provider matches the type.** A Messenger channel's `provider` is the key of a filled
`ChannelMessenger` (N2). An e-mail channel's provider stays SMTP or a mailbox. A mismatch is
422 `provider_type_mismatch`.

**Dispatch by slot.** `isMailboxProvider(p) = p != "smtp"` (`contractscalls.go:48`) is
replaced by a lookup in the mailbox slot and the messenger slot. A key in neither gets the
existing defensive 422. Compose refuses a key that is filled in both slots.

**Defaults become per type.** Today they are not:

- `ClearDefaultChannels` demotes every default (`queries/channels.sql:87`);
- `ClearOtherDefaultChannels` is type-unfiltered (design divergence 14);
- `AnyChannelExists` (the "first channel is forced default" rule) counts every channel.

All three gain `WHERE type = @type`. The global advisory lock of divergence 15 stays, and still
serialises every writer. Without this change, the first Messenger default would demote the
e-mail default that Invoices and Calendar send through. This is a behaviour change, and it is
written down as divergence 16 in the module's design, and in `Ref/communications.md`.

**The mailbox worker skips Messenger channels.** The mailbox sync's candidate and claim
queries pick channels by `c.provider <> 'smtp'` (`queries/mailbox_sync.sql:14`, `:29`). Both
become `c.type = 'email' AND c.provider <> 'smtp'`, so a Messenger channel is never claimed by
the mailbox cycle. No `channel_sync` row is created for a Messenger channel.

No migration is needed for the type itself.

### N2 — The slot `contracts.ChannelMessenger`

It holds many providers, and is collected the way `ChannelMailboxes` are: through
`module.Module.ChannelMessenger` into `Deps.ChannelMessengers`.

```go
type ChannelMessenger interface {
    Key() string                                    // "meta"
    DisplayName() string                            // "Facebook Messenger", as ChannelMailbox
    Status(ctx) (MessengerStatus, error)            // the provider: available, or the reason
    Capabilities() MessengerCapabilities            // the limits of N4, the window of N6
    Endpoints(ctx) ([]MessengerEndpoint, error)     // the Pages connected, each with its own status
    Check(ctx, address string) (MessengerEndpoint, error) // one Page: name, available or the reason
    Send(ctx, address string, part MessengerPart) (MessengerSent, error)
    Notified(ctx, since time.Time) ([]string, error) // the addresses with new events
    Pending(ctx, address string, after int64, limit int) ([]MessengerEvent, error)
    OpenAttachment(ctx, address, ref string) (io.ReadCloser, MessengerFile, error)
    Ack(ctx, address string, upTo int64) error
    Profile(ctx, address, participant string) (MessengerProfile, error)
}
```

Every method is a provider call. Communications calls them outside any transaction, under the
module's existing `errCallInsideTransaction` rule.

**The cursor** is the provider's own insertion id, a bigserial. It is never a timestamp,
because webhooks arrive out of order. The timestamp only orders the display.

**Attachments cross the boundary through `OpenAttachment`,** never as a storage key. The import
copies the bytes into Communications' own storage before its transaction. After `Ack`, the
provider deletes its own copy.

**Events have three kinds:**

- **`message`** carries text, attachments, an optional answer (N5), the participant's platform
  id, a timestamp and the platform message id. An echo (N12) is a `message` marked `echo`.
- **`delivered`** carries a watermark. Every outbound message on that participant's
  conversation sent at or before the watermark becomes delivered.
- **`read`** carries a watermark, and works the same way for read.

Watermarks are monotonic and their effect is idempotent. They do not go through
`inbound_receipts`, whose outcome CHECK is shaped by the mailbox.

### N3 — The messenger sync worker

`communications-messenger-sync` is a new worker of its own. It is not a rename of the mailbox
worker, whose name operators see in `A/installation.md`.

**Its state** is `communications.messenger_sync`, added in migration *A1*:

- the key is `channel_id`;
- `cursor bigint`;
- `notified boolean`;
- `next_sync_at`, `lease_id`, `lease_until`;
- `state`, with CHECK `active` / `failed` / `stopped`;
- `last_error`.

A row is created with the channel.

**The cycle:**

1. Every 2 s it asks each provider for `Notified(since)`, and marks those channels due. A
   channel is also due every 60 s regardless.
2. It claims one due channel by conditional update, the same way the mailbox sync does.
3. It reads `Pending(after = cursor, 100)`.
4. For each event, outside any transaction:
   - it fetches the attachments through `OpenAttachment` into storage;
   - it fetches the participant's `Profile` when the participant is new or its name is older
     than 30 days.
5. It writes in one transaction:
   - the receipt, which dedupes by `(channel_id, provider_message_id)`;
   - the participant;
   - the threading (N3a);
   - the message;
   - the cursor advance.

   An object stored for an event whose transaction rolls back gets an
   `attachment_cleanup_records` row first, as staging does today.
6. It calls `Ack(upTo = cursor)`.

A `Pending` or `Ack` failure backs the channel off exactly as the mailbox sync does.

### N3a — Threading and participants

**The participant.** A Messenger participant's `address` is the PSID, the id Meta gives the
person for this Page. Its `display_name` comes from `Profile`.

**Threading.** An inbound message joins the newest **open** conversation of the same channel
and participant. Otherwise it starts a new one. A closed conversation stays closed.

**No subject.** A Messenger conversation has no subject. Its title is the participant's name.

**The customer link** is manual, through the existing *Link customer*. Messenger gives no
e-mail address, so the e-mail contact linker does not run. The agent can send the quick reply
*Share e-mail* (N8). The address that comes back runs through the contact linker as a
suggestion.

### N4 — Components on a message

**Storage.** Migration *A1* adds `conversation_messages.components jsonb`, and
`message_attachments.role` with CHECK `file` / `card` and default `file`.

**In the contract**, on the message response and on the reply request:

```yaml
MessageComponents:
  answerOptions:            # quick replies, in the order given
    - { id: string, title: string }       # 1..13; title 1..20 characters
  shareEmail: boolean                     # Messenger's user_email quick reply
  cards:                    # a carousel (delivery C)
    - title: string                       # 1..80
      subtitle: string?                   # ..80
      productImageId: int32               # request only (N16)
      attachmentId: uuid                  # response only: the card's image, role 'card'
      choices:                            # 0..3 per card
        - { id: string, title: string }   # title 1..20
```

**Ids.** An `id` is `[a-z0-9-]{1,40}` and unique within its message. The payload sent to the
platform is `v1:<messageId>:<id>`, well under Meta's limit of 1000.

**The limits are the provider's `Capabilities`.** Reply checks the components against them:

- a channel without components (every e-mail channel) gets 422 `components_unsupported`;
- a limit broken gets 422 `components_invalid`, with `fields`.

**Idempotent replay.** `replyFingerprint` (`conversations_reply.go:218`) includes the
components and the card images. An idempotent replay with different components is the
existing mismatch refusal.

**Old messages** have `components: null`, and are unchanged.

### N5 — Answers

**What arrives.** When the customer taps an answer option or a card's choice, Meta sends a
quick-reply payload or a postback. The provider returns it as a `message` event with
`answer: { messageId, choiceId }`, parsed from the payload, and with the title the customer saw
as its text.

**What is stored.** The inbound message stores `answer` in `channel_metadata_json`. It is
exposed as `answerTo: { messageId, choiceId, title }`.

**How the thread shows it.** The answer shows as a chip under the question ("✓ Yes"), and the
question marks the chosen option.

**A payload the server cannot place** (an unknown version, or a message outside this
conversation) becomes plain text and is logged at info.

**`user_email` answers** carry the address as text. They are marked `answerTo.choiceId =
"share-email"`, and feed the contact linker (N3a).

### N6 — The reply window

**The window.** A Messenger conversation's window is open for 24 h after the participant's
newest inbound message. Echoes do not count.

**In the contract.** The conversation detail exposes
`replyWindow: { state: "open" | "human_agent" | "closed", closesAt }`.

**The human-agent extension.** The provider's `Capabilities` say whether the app has Meta's
Human Agent permission, from the setting in N10. When it does, the window runs to 7 days, and
a reply after the first 24 h carries the `HUMAN_AGENT` tag.

**When the window is closed:**

- Reply answers 422 `reply_window_closed`.
- The composer disables sending, and says "The customer must write again before you can answer
  here".
- The outbox checks the window again at send time. A job that missed it fails terminally with
  the reason, and does not retry.

### N7 — Sending in parts, through the outbox

**The parts.** The outbox gets a messenger branch beside SMTP and the mailbox. Messenger takes
one attachment per message, so a message is sent as ordered parts:

1. the text, if any;
2. each `file` attachment;
3. the carousel, made from the `card` attachments and the cards.

The answer options ride on the last part. `card` attachments are never sent as parts of their
own.

**Progress per part** is held in `communications.messenger_parts`, added in migration *A1*:

- the key is `(message_id, part_index)`;
- `attempted_at`;
- `platform_message_id`;
- `sent_at`.

Each part is written in its own statement, outside any transaction:

- `attempted_at` immediately before `Send`;
- `platform_message_id` and `sent_at` immediately after.

**A job claimed again** skips the parts already sent. A part with `attempted_at` and no
`sent_at` is the per-part "possible duplicate": it is logged, counted in the existing metric,
and sent again. This is the job-level marker's rule, applied to each part.

**The lease.** Each `Send` has the existing `outboxSendTimeout` (30 s). Before every part after
the first, the job's lease is renewed by a conditional update on its `lease_id`. A renewal
that loses its lease stops the job without sending.

**Matching answers and echoes.** `platform_message_id` is how answers (N5) and echoes (N12) are
matched to the message.

**What does not apply on Messenger.** Reply refuses or ignores the e-mail-only inputs:

- `sendFromPersonal` and `replyMode: all` get 422 `not_supported_on_channel`;
- `subject` is ignored;
- `mailEstimate` is not run;
- text over 2000 characters gets 422 `text_too_long`.

Conversation create refuses a Messenger `channelId` with 422 `channel_cannot_start`, because
the person always writes first.

**Suppressions** stay e-mail only. A person who blocks the Page shows as the terminal failure
`recipient_unreachable`.

### N8 — The frontend

**The channel form** chooses the type first:

- *E-mail*, as today;
- *Facebook Messenger*, where the Page is picked from `Endpoints`. This type is offered only
  when a messenger provider is available.

**The thread** is rendered by channel type. A Messenger conversation shows:

- chat bubbles;
- images inline, as data URIs, as today;
- cards as a carousel;
- answer options as chips;
- answers under their question;
- echoes marked "Sent from Meta".

E-mail is unchanged.

**The composer** on a Messenger conversation is a chat composer:

- plain text, with no subject, no Cc and no rich formatting;
- attachments;
- the window's state.

Answer options are added in the composer:

- *Add Yes / No* adds two options, Yes and No in the user's language, which the agent can
  rename.
- *Add option* adds one more, up to 13.
- Each option has a remove ×, and a counter against the 20-character limit.
- The options keep the order they were added in.
- *Ask for e-mail* sets `shareEmail`.

**The inbox** shows the channel's icon on each row.

**New conversation** stays e-mail only.

## 3. Decisions — delivery B, the Meta module

### N9 — A new integration module, `meta`

**The module.** It is Meta (Norwegian *Meta*), package `@vantigo/meta-ui`. It owns the schema
`meta` and fills `ChannelMessenger` under the key `meta`, with the display name "Facebook
Messenger".

**Registries.** It is added through the module checklist in `AGENTS.md`:

- the depguard sets;
- `everyModule()`;
- the Assistant's link prefixes and `promptVersion`;
- the host's registrations and `sessionClients`.

**Config.** It requires `communications` in `internal/config`, and so joins `modulesEnv`.

### N10 — Settings under Integrations

The settings are in the workspace tier, and the Owner sets them on a *Meta* page under
Integrations.

**When the page refuses.** It refuses to save while the instance has no public host configured.
The webhook lives on the public host.

**The fields:**

- **App ID**, and the **App secret**. The secret is sealed under the purpose `meta/app-secret`.
- **Verify token.** Generated and shown for copying. *Regenerate* replaces it.
- **Webhook URL.** Shown for copying: `https://<public host>/api/v1/meta/webhook`.

**Pages.** *Add Page* takes a Page access token. It is sealed under the purpose
`meta/page-token`. Vantigo then:

1. reads `/me`, and checks that the token belongs to a Page and may message it, else answers
   422 `page_token_insufficient`;
2. subscribes the app to the Page's fields: `messages`, `messaging_postbacks`, `message_reads`,
   `message_deliveries` and `message_echoes`;
3. stores the Page's id and name.

The page recommends a token that does not expire, a system user's token. A Page can be removed.
A channel on a removed Page, or on a Page whose token Meta rejects (code 190), becomes
unavailable with the reason, through `Check` and `Endpoints`.

**Human Agent.** A checkbox: "Meta has granted this app the Human Agent permission". It drives
N6. A tagged send that Meta refuses for permission (code 10 or 200 on a tagged send) is the
terminal failure `human_agent_not_permitted`. The status line then tells the Owner to clear the
checkbox.

**Status.** The page shows when the last webhook arrived, whether it was verified, and the last
error from the Graph API.

**The `A/meta.md` procedure** walks through, step by step:

- creating the app in Meta's developer portal;
- App Review and Business Verification, as delivery 0 found them;
- the privacy policy URL and the data-deletion instructions URL that Meta requires of the
  business's app.

### N11 — The webhook

The webhook is `GET` and `POST /api/v1/meta/webhook`. Both are declared in `openapi/meta.yaml`
as `x-vantigo-access: anonymous`, with their own rate-limit policy, and both are added to
`security.PayRoutes()`.

**GET** checks `hub.mode=subscribe`, and compares `hub.verify_token` in constant time. It
answers `hub.challenge` as `text/plain` with 200, or 403.

**POST** is a hand-written handler, outside the strict wrapper:

1. It reads the raw body, up to 1 MiB.
2. It checks `X-Hub-Signature-256: sha256=<hex>`, an HMAC-SHA256 under the app secret over the
   bytes as received. The comparison is in constant time. A missing or wrong signature answers
   401, and is logged.
3. It stores the body in `meta.webhook_events`, with the time received, the raw jsonb and a
   state, and answers 200.
4. If the database cannot be written, it answers 503, so Meta retries.

It parses nothing for routing before answering, because Meta wants an answer within 5 s.

**A worker in `meta`** splits each stored event into its messaging entries per Page, into
`meta.inbound`, keyed by a bigserial cursor:

- **Messages** are deduplicated by Meta's `mid`.
- **Postbacks** are deduplicated by `postback.mid`.
- **Read and delivery events** are deduplicated by (Page, PSID, kind, watermark).

Inbound images and files are fetched from Meta's CDN at once, because those URLs expire. They
are stored under the storage scope `meta-inbound`.

`Notified` answers the Pages that have rows not yet acknowledged.

**Retention:**

- raw events are deleted after 7 days;
- acknowledged rows and their stored files after 7 days;
- profile cache entries after 30 days.

### N12 — Sending and echoes

**Sending.** `Send` calls `POST /{page-id}/messages`. Its `messaging_type` is `RESPONSE`, or
`MESSAGE_TAG` with `HUMAN_AGENT` (N6).

**Each part's content:**

- File parts are uploaded with the multipart Send API (`filedata`), so storage needs no public
  URL.
- Answer options become `quick_replies` of type `text`.
- `shareEmail` becomes a `user_email` quick reply.
- Cards become a generic template (delivery C).

**Graph errors** map to the outbox's two outcomes:

| Error | Outcome |
| --- | --- |
| 1, 2, 4, 613 and 5xx | Temporary |
| 551 | Terminal: `recipient_unreachable` |
| (10, subcode 2018278) | Terminal: `reply_window_closed` |
| 190 | Terminal; the Page becomes unavailable |
| 10 or 200 on a tagged send | Terminal: `human_agent_not_permitted` |
| Anything else | Terminal, with Meta's message |

**Echoes.** An echo whose `mid` is in `messenger_parts` is ignored. Any other echo was sent from
Meta Business Suite or another app on the Page. It is imported as an outbound message marked
"Sent from Meta", so the thread is whole.

### N12a — Personal data in `meta`

`meta` holds PSIDs, names, message text and files only within the retention of N11, at most 30
days. It cannot implement `contracts.CustomerPersonalData`, because it does not know customer
ids. **Retention is the only bound on it.** `Ref/meta.md` and `A/meta.md` say so.

Communications' existing erasure covers its own copies. Participant rows (the PSID and the name)
outlive a customer erasure until retention's orphan sweep (`retention.sql:105`), the same as an
e-mail participant does today. `Ref/communications.md` says so.

## 4. Decisions — delivery P, product images

### N13 — Images on a product

**The table.** Migration *P1* adds `products.product_images`:

| Column | |
| --- | --- |
| `id` | serial `int32`, as the module's other ids |
| `product_id` | `ON DELETE CASCADE` |
| `storage_key`, `thumb_storage_key` | |
| `content_type`, `byte_size`, `width`, `height`, `sha256` | |
| `alt_text` | Nullable, at most 250 characters |
| `sort_order` | |
| `is_primary` | At most one per product, by a partial unique index |
| `uses text[]` | `NOT NULL DEFAULT '{communications}'`, CHECK `uses <@ ARRAY['communications','marketing']` |
| `created_at`, `created_by` | |

**What upload accepts.** JPEG and PNG, at most 10 MiB, and at least 200 × 200 px. WebP and HEIC
get 422 `image_type_unsupported`.

**Normalisation at upload.** Upload normalises every image before it is stored. Only the
normalised file is kept.

1. `image.DecodeConfig` reads the size before anything is decoded. More than 40 megapixels gets
   422 `image_too_large`, so a decompression bomb costs nothing.
2. The EXIF orientation is read, through a new `imagemeta.Orientation`.
3. The image is decoded, rotated upright, scaled so its longer side is at most 2048 px, and
   encoded again: JPEG at quality 90, or PNG.
4. Encoding again drops all metadata, so no camera location reaches a customer.
5. A 320 px thumbnail is made in the same pass, with `golang.org/x/image/draw`, which is
   already a dependency.

**Storage.** Both files go in the object store under the scope `products-images`.

**Endpoints.** No new permissions; the ten that exist cover it.

| Endpoint | Does | Permission |
| --- | --- | --- |
| `GET /products/{id}/images` | Lists the images | `products:products-view` |
| `POST /products/{id}/images` | Uploads (multipart) | `products:products-manage` |
| `PATCH /products/{id}/images/{imageId}` | Changes the alt text, `sortOrder`, `isPrimary` and `uses` | `products:products-manage` |
| `DELETE /products/{id}/images/{imageId}` | Deletes the image | `products:products-manage` |
| `GET /products/{id}/images/{imageId}/content` | Streams the image; `?size=thumb` streams the thumbnail | `products:products-view` |

**The uses.**

- `communications` means the image may be shown to a customer in a message. The composer
  offers only these images.
- `marketing` is stored, edited and documented as "for marketing use, such as ads. Nothing reads
  it yet".

**The primary image.** The first image uploaded becomes primary. Deleting the primary promotes
the next one by `sort_order`.

### N14 — The product page

The product page gets an *Images* section:

- drop files, or pick them;
- thumbnails in order, each with *Move left* and *Move right*;
- a star for the primary image;
- for each image, its alt text and two checkboxes, *Show in Communications* and *Show in
  marketing*;
- delete.

### N15 — Images on `contracts.ProductCatalog`

Two calls are added:

```go
ProductImages(ctx, productID int32, use string) ([]ProductImage, error) // in order; empty when none
OpenProductImage(ctx, imageID int32) (io.ReadCloser, ProductImage, error) // nil, zero, nil when gone
```

`ProductImage` carries `ID`, `ProductID`, `AltText`, `ContentType`, `Width`, `Height`,
`Uses` and `SortOrder`.

Communications reads through these, never through the products schema. Like the rest of the
catalog contract, the calls check no user permission. The composer's picker is what checks the
user (N16).

## 5. Decisions — delivery C, a product in a message

### N16 — Attach product

**The picker.** *Attach product* in the chat composer opens a product picker. The picker runs
on the Products API with the user's own permissions:

- it needs `products:products-view`;
- it is hidden without that permission, or without the Products module.

**One product per message.** The agent picks one product. Its images marked `communications`
become the cards, at most 10, in their order. Each card has:

- **title:** the product's name;
- **subtitle:** prefilled with the product's price, when the user has
  `products:pricing-view`. The frontend reads it through the Products API, with the same VAT
  label the product page shows. The agent can edit or clear the subtitle. The server treats it
  as text of at most 80 characters, and derives no price itself;
- **choices:** *Buy* (id `buy`), titled *Kjøp* or *Buy* by the message's language. Optionally a
  second choice that the agent writes.

**Before sending.** The composer shows the carousel, and the agent can remove cards. A product
with no image marked for Communications cannot be attached, and the picker says why.

**The copy, at reply time.** The reply request names each card's `productImageId`. Before the
reply transaction, reply:

1. checks each image through `ProductImages(productID, "communications")`; one not there gets
   422 `product_image_unavailable`;
2. opens it, and stores the bytes in Communications' storage, with an
   `attachment_cleanup_records` row written first;
3. creates the message's `card` attachments in the transaction.

The message keeps what was sent, even when the product's images change later.
`channel_metadata_json` keeps the product id for N17.

**Answer options** may be added to the same message. Meta allows quick replies on a template.

### N17 — Buy, back to the agent

A *Buy* answer (N5) shows in the thread as "Wants to buy: <product>", with the card's image.

When the Orders module is enabled and the user may create orders, the answer carries a *Create
order* link to the new-order page. The page opens with:

- the conversation's linked customer, if there is one;
- the product's variant prefilled when the product has exactly one, and otherwise the variant
  picker opened on that product.

No order is made automatically.

### N18 — The public media route

**Why it exists.** Meta's generic template takes an `image_url` that Meta fetches, so the image
must be public. Storage has no public URL, by design.

**The route.** Communications adds `GET` and `HEAD /api/v1/communications/public-media/{token}`.
It is anonymous, on the public host, and added to `PayRoutes()`.

**The token** is `<attachmentId>.<expiresAt>.<mac>`, with the MAC from the secrets Box's `MAC`
under the purpose `communications/public-media`. The token expires a time after the send that
delivery 0 decides (§8 Q2), 7 days unless 0 says otherwise.

**What it serves.** Only a `card` attachment of an outbound message, on a channel whose provider
asks for public media. It answers with:

- the image's content type;
- `Referrer-Policy: no-referrer`;
- `Cache-Control: private, max-age=86400`.

**Refusals.** An expired, wrong or foreign token answers 404, never 401, so the route cannot be
used to probe.

**Logging.** Each fetch is logged with the attachment id, never the token.

## 6. Permissions

No new permissions.

| Action | Permission |
| --- | --- |
| A Messenger channel | `communications:channels-manage` |
| The Meta settings | The Owner, as every Integrations page |
| Replying | `communications:conversations-reply` |
| Attaching a product | `products:products-view`, plus `products:pricing-view` for the prefilled price |
| Changing product images | `products:products-manage` |
| Seeing product images | `products:products-view` |
| *Create order* | Orders' own create permission |

## 7. Documentation

Each delivery changes its pages in the same commit series as its code. For `U/` and `A/`,
English comes first and bokmål follows. Each new migration and source path joins the `sources`
frontmatter of the pages that describe it. `Ref/products.md`, for instance, lists migrations one
by one.

**Delivery A:**

- `U/communications.md`: Messenger conversations, answer options and the window.
- `Ref/communications.md`: the types, defaults per type (divergence 16), components, answers,
  the window, the slot, the messenger sync worker, `messenger_parts`, the new 422s, and the
  participant orphan note.
- `A/installation.md`: the new worker.

**Delivery B:**

- A new `A/meta.md`, en and nb: the procedure for creating the app, App Review and Business
  Verification, the privacy and data-deletion URLs, the settings, the webhook, Page tokens, the
  Human Agent permission, retention, and what can go wrong.
- `A/index.md`: lists Meta.
- `A/installation.md` and `A/payments.md`: the public host's new route.
- `U/communications.md`: a Messenger channel.
- A new `Ref/meta.md`: the module, the webhook and its signature, the inbound queue, echoes,
  retention and personal data, the workers, and the error mapping.

**Delivery P:**

- `U/products.md`: images and their usage flags.
- `Ref/products.md`: the table, the endpoints, normalisation, and the catalog calls.

**Delivery C:**

- `U/communications.md`: *Attach product* and *Buy*.
- `Ref/communications.md`: the copy at reply time, and the public media route and its token.
- `A/installation.md` and `A/payments.md`: the media route on the public host.

After any docs edit, run `go generate` in `apps/server`, because the Assistant embeds the docs.

## 8. Open questions, answered by delivery 0

1. **Does an app owned by the business need Advanced Access to message the public?** Advanced
   Access to `pages_messaging` means App Review and Business Verification. 0 answers it with
   Meta's documentation and a live test: a test Page messaged by an account with no role on the
   app.
   - If the answer is no, B proceeds as written.
   - If the answer is yes, `A/meta.md` says so plainly, and the owner chooses between two
     paths. Either the pilot customer goes through App Review once, or the app owned by
     Vantigo, with its relay, moves into this phase.
2. **Does Meta keep a template's image after fetching it?** 0 sends a carousel, lets the image
   URL expire, and reopens the conversation on a fresh device.
   - If Meta keeps it, N18's 7 days stands.
   - If not, the expiry becomes long (a year), and `Ref/communications.md` says why.
3. **Does the `HUMAN_AGENT` tag work for an EEA user?** 0 tests it if the permission can be had
   in time. Otherwise B tests it.

The owner creates the Meta developer account, the test app and the test Page. The agent cannot
create accounts.

*(R's question on the 2000-character text limit is closed: the Send API reference states it.)*

## 9. Later, and what each waits for

| Item | Waits for |
| --- | --- |
| Instagram | A second messaging provider in `meta`, on the same slot and webhook |
| A Meta app owned by Vantigo, and Facebook Login | A central relay service, the same deferred piece as the hosted mail service |
| SMS | A provider (LINK Mobility or Sinch), with components degraded to numbered text |
| Several products in one carousel, and checkout from the chat | A public order-and-pay page |
| Answer options in e-mail | A public answer page |
| Images of a single variant, and product-list thumbnails | A need in Orders or a webshop |
| Reordering answer options by dragging, and an inbox filter by type | Use |
| The marketing flag | A marketing or advertising feature to read it |
| Bots and automatic replies | Their own design |

## 10. Testing

**Delivery A.** Communications' harness gets a fake `ChannelMessenger`. The tests cover:

- **Channels:** create a channel; the defaults per type, including that a Messenger default
  leaves the e-mail default alone; the mailbox sync never claiming a Messenger channel.
- **Sync:** `Pending` and `Ack` with the cursor; idempotent import on replay; out-of-order
  events; attachments through `OpenAttachment`, including a rollback leaving a cleanup record;
  `Profile` called outside the transaction, where the `errCallInsideTransaction` guard trips if
  it is not; watermarks.
- **Threading:** joining the open conversation, and a closed one staying closed.
- **Replies:** answers; the fingerprint including the components; the window at 24 h, at 7
  days and closed; every new 422.
- **Outbox:** sending in parts; a reclaim after part 2 of 3 skipping the parts already sent; a
  part attempted but not sent being counted as a possible duplicate; lease renewal, and a lost
  lease stopping the job.
- **Concurrency:** two syncs racing for one channel, and two outbox workers racing for one job,
  using the established `pg_stat_activity` gate.

**Delivery B:**

- **The webhook:** the GET handshake; HMAC over the raw bytes, with a fixture signed as in
  Meta's documentation, and the same body re-serialised, which must fail; the 1 MiB cap; 503
  when the database is down.
- **The `meta` worker:** splitting events, the dedup keys, and ordering.
- **The Send API** against an `httptest` fake on `HTTPTransport`, including every row of the
  error map.
- **Pages:** adding and removing a Page, and the token checks.
- **Echoes:** an echo of our own being ignored, and a foreign one imported.
- **Retention.**

**Delivery P:**

- type, size and pixel-cap refusals;
- EXIF orientation applied and metadata gone;
- the thumbnail;
- one primary per product, and promotion when the primary is deleted;
- the CHECK on `uses`;
- the catalog calls.

**Delivery C:**

- the carousel request, with up to 10 cards;
- an image not marked for Communications refused;
- the copy surviving deletion of the product's image;
- the media route: a valid token, an expired one, one for another attachment, one for a `file`
  attachment and one for an inbound attachment, all but the first answering 404; HEAD;
- *Buy* with the *Create order* link when Orders is present, and without it otherwise.

**The browser check** in B and C runs against the test Page from delivery 0.

## 11. Revision 2 — the critic review, 2026-11-05

- **The messenger sync is a worker of its own (N3).** The mailbox queries select
  `provider <> 'smtp'`, and so would have claimed Messenger channels. They now also filter on
  `type = 'email'`. The cursor is an insertion id, not a timestamp. Watermark events stay out
  of `inbound_receipts`. `Notified` and `OpenAttachment` are added to the slot, and the profile
  is fetched before the transaction.
- **Sending in parts records progress per part (N7).** The parts are in `messenger_parts`, and
  the lease is renewed between parts. Card images are never sent as parts of their own, which
  removes the contradiction between N7 and N16.
- **Defaults are per type (N1).** `ClearDefaultChannels`, `ClearOtherDefaultChannels` and
  `AnyChannelExists` are type-unfiltered today, and a Messenger default would have demoted the
  e-mail default.
- **The contract is `contracts.ProductCatalog`, with `int32` ids (N15).** `imagemeta` does not
  read dimensions, so upload now normalises the image: a pixel cap, the orientation applied,
  re-encoding and a thumbnail (N13).
- **Card images are named by `productImageId` and copied at reply time,** before the
  transaction (N4, N16). Attachments are uuids, and carry `role`.
- **Reply on Messenger:** the fingerprint includes the components, the e-mail-only inputs are
  refused, and create refuses a Messenger channel (N7).
- **Personal data in `meta`** is bounded by retention, and documented (N12a).
- **Price:** the subtitle is text prefilled by the frontend under the user's own
  `pricing-view`. The server derives no price (N16).
- **Meta's facts:**
  - code 2018278 is a subcode of 10;
  - code 190 means the Page is unavailable;
  - Human Agent is a permission, and a wrong checkbox is a terminal failure;
  - `message_echoes` is now subscribed;
  - the text limit is confirmed.
- **The App Review question gates B, as delivery 0** (§8), instead of being B's first task.
  0 also tests the caching of images.
- **Minor fixes:**
  - the secret purposes are `module/thing`;
  - the media token is MAC'd, not sealed;
  - HEAD is allowed on the media route;
  - the webhook is declared in `openapi/meta.yaml`;
  - the settings page requires a public host;
  - Compose refuses a key filled in both slots;
  - `DisplayName()` matches the mailbox slot;
  - postbacks are deduplicated by `postback.mid`.
- **Documentation:** `A/installation.md` (the worker, the routes), `A/index.md`, `A/payments.md`
  and `A/meta.md` in nb are added. The rule for `sources` frontmatter is stated.
- **Cut from scope:** images of a single variant, reordering options by dragging, product-list
  thumbnails, and the inbox filter by type.
