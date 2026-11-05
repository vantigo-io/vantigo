# Communications — direct-messaging channels and rich messages (research)

2026-11-05. The question: can Communications, which today handles e-mail through SMTP,
Microsoft 365 and Google Workspace, take a direct-messaging channel such as Facebook
Messenger, and can a message carry buttons, images and quick answers? This note gathers what
the code and the platforms say. The design that follows from it is
[`2026-11-05-communications-messenger-design.md`](../specs/2026-11-05-communications-messenger-design.md).

Abbreviations: `srv/` stands for `apps/server/internal/`, `mig/` for `srv/db/migrations/`.

## 1. What the module is today

### 1.1 Schema

The schema takes a new channel almost without change:

- **Channel type and provider.** `communications.channels` has a free `type varchar(30)` and
  `provider varchar(20)`, with no CHECK on either (`mig/00006`). The uniques are
  `(type, address)` and one default per type.
- **Credentials.** `channel_credentials` holds `settings_json` and a sealed
  `secret_ciphertext`.
- **Participants.** `participants.address varchar(320)` has no e-mail CHECK, so a Messenger
  page-scoped ID (PSID) fits.
- **Messages.** `conversation_messages` has a `direction` CHECK of `inbound`, `outbound` and
  `internal_note`; `subject`, `text_body` and `html_body`; and a free
  `channel_metadata_json jsonb` that the contract does not expose.
- **Receipts.** `inbound_receipts` (`mig/00092`) is unique on
  `(channel_id, provider_message_id)`, so it serves webhook deduplication as it is.
- **The e-mail-shaped tables.** These are `suppressions` (`normalized_email_address`), and
  `channel_sync` and `mailbox_marks`, which are shaped by a mailbox.

### 1.2 Code

The code is where e-mail is assumed:

- **Type and address.** `channels.go:400` refuses every type but `email`, and
  `channels_validation.go:39` requires an RFC 5322 address.
- **Provider dispatch** is binary. `isMailboxProvider(p) = p != "smtp"`
  (`contractscalls.go:48`) means every provider that is not SMTP is taken for a mailbox.
- **The provider slot.** `contracts.ChannelMailbox` (`contracts/channel_mailbox.go`) is MIME
  and mailbox shaped throughout: Send takes MIME bytes, and Delta, Raw, Mark and EnsureFolder
  are mailbox operations. A messaging provider needs its own slot.
- **The outbox** (`outbox.go`) builds a `mail.Outbound` envelope for every job. It has no
  sender interface that is neutral of the channel.
- **Threading** (`inbound_threading.go`) goes In-Reply-To → References → provider thread id.
  A messaging channel threads by channel and participant instead.
- **The size budget** (`mailEstimate`) and the reply's subject and Cc handling are specific to
  MIME.

### 1.3 Contract and frontend

- **Contract.** `openapi/communications.yaml` has no `enum:` at all; `type` and `provider` are
  free strings. A message is `subject`, `textBody` and `htmlBody`. Nothing in it is structured
  or rich.
- **Thread view.** `ConversationThread.tsx` shows the HTML in a sandboxed iframe, or else the
  text.
- **Composer.** `ConversationComposer.tsx` is TipTap, with a choice between reply and reply-all
  with Cc.
- **Channel form.** `ChannelForm.tsx` and `api/channels.ts` hard-code `type: "email"`.

### 1.4 Public callbacks

- **Gating.** An anonymous operation is marked `x-vantigo-access: anonymous`. On the public
  host only `security.PayRoutes()` passes (`security/publichost.go`).
- **The existing callbacks are nudges.** The Entra and Google notification endpoints mark a row
  notified, and a worker pulls the truth. No endpoint today verifies an HMAC over the raw body.
- **What that means for Meta.** Meta signs with the app secret over the bytes it sends, so the
  signature must be checked over the body exactly as received, before any decoding. The
  generated strict wrapper decodes JSON first, so the endpoint needs a handler of its own.

### 1.5 Products

Products has no images. The module has the platform object store through
`module.Deps.ObjectStore` / `storage.New`, and `srv/imagemeta` exists for reading image
metadata.

## 2. The platforms (checked against their own documentation, November 2026)

Uncertain points are marked *(unverified)*.

### 2.1 Facebook Messenger, chosen

**The app.** A Meta app with the Messenger product and a Page access token.

**Webhook.** It is set up with a GET handshake: `hub.mode=subscribe`, `hub.verify_token` and
`hub.challenge`. Answer with the challenge, or 403. After that:

- Every POST carries `X-Hub-Signature-256: sha256=<HMAC-SHA256(app secret, body)>`.
- Answer 200 within 5 s.
- Meta retries, so deduplicate.
- Events may come out of order, so order them by `timestamp`.
- After an hour of failures Meta unsubscribes the app.
- The events are `messages`, `messaging_postbacks`, `message_reads` and `message_deliveries`.

**Window.**

- Standard messaging is open for 24 h after the person's last message.
- The `HUMAN_AGENT` tag extends that to 7 days. It needs its own permission through App Review,
  and is only for real human support.
- Meta deprecated `CONFIRMED_EVENT_UPDATE`, `ACCOUNT_UPDATE` and `POST_PURCHASE_UPDATE` on
  2026-03-27; they have returned error 100 since 2026-04-27.

**Components.**

| Component | Limits |
| --- | --- |
| Quick replies | Up to 13. Title up to 20 characters, payload up to 1000. Kinds `text` (optionally with an image), `user_email` and `user_phone_number` |
| Button template | 1–3 buttons, text up to 640 characters |
| Buttons | `postback`, `web_url`, `phone_number`, … |
| Generic template (carousel) | Up to 10 elements, each with up to 3 buttons. Title and subtitle up to 80 characters each. Image 1.91:1, given as `image_url` |
| Media attachments | Supported |
| Sender actions | `typing_on`, `mark_seen`, … |

**Access.** Messaging the public through a Page needs `pages_messaging` with Advanced Access,
which means App Review and Business Verification of whoever owns the app. *(Unverified:
whether an app of the business's own, used only for its own Page, can go live without Advanced
Access. Design §8 Q1.)*

**EEA.** The 2020 restrictions were restored in 2020–2022, and no EEA change is listed for
2024–2026 *(test with an EEA user)*.

### 2.2 Instagram

Instagram uses the same webhook and signature model as Messenger, and nearly the same
components. It needs a professional account and `instagram_business_manage_messages`. It is a
later, cheap addition on top of Messenger.

### 2.3 Not now

- **WhatsApp** is set aside by the owner, because little of Norway uses it for business. It
  would mean the Tech Provider or BSP onboarding, approval of each template, and per-message
  prices: service replies are charged beyond 1,000 a month since 2026-10-01.
- **SMS** reaches everyone and has no components. Two-way SMS needs a real number.
- **RCS** support in Norway is unverified.
- **A web-chat widget** on the business's own site has no approval, no window and every
  component, but it only reaches visitors while they are on the site.
- **Google Business Messages** was shut down in July 2024.
- **Apple Messages for Business** needs an Apple-approved messaging service provider.

## 3. Whose Meta app

**A. Vantigo owns one app.**

- **Vantigo does, once and ongoing:**
  - the Business Verification of Vantigo AS;
  - App Review, with a screencast, a privacy policy, a data-deletion callback and a
    justification for each permission;
  - Meta's yearly data-use check;
  - following Meta's policy changes.
- **The customer does:** they click *Connect Facebook Page* and choose the Page.
- **The cost.** Vantigo runs one deployment per customer, but a Meta app has one webhook URL. A
  central service would therefore have to:
  - receive every Messenger webhook and verify it;
  - route each event by Page ID to the customer's deployment;
  - host the Facebook Login redirect.

**B. The customer owns the app.** This is the model of the Entra and Google integrations
today.

- **The customer does:**
  - creates the app and adds Messenger;
  - enters the webhook URL and the verify token that Vantigo shows;
  - pastes the app secret and a Page token into Vantigo.
- **Vantigo does:** each deployment takes its own webhook on its public host, and nothing is
  central.
- **The cost.** If going live needs Advanced Access (§2.1), every customer goes through App
  Review.

**The owner chose B for phase 1 (2026-11-05)**, with the provider built so that A later
changes only the onboarding.

## 4. A component model neutral of the channel

The model is designed to the smallest limits, so that it carries to later channels:

- labels of 20 characters;
- 13 quick replies;
- 3 buttons on a card;
- 10 cards.

Every choice carries a stable `id`. The customer's answer comes back as an inbound message that
names the choice. On a channel without components (SMS later) the model degrades to numbered
text, and the answer is matched back to the last menu sent.
