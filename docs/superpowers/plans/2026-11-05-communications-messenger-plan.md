# Communications — Messenger, answer options and product images: implementation plan

This plan builds the design in
`docs/superpowers/specs/2026-11-05-communications-messenger-design.md` (revision 2, decisions
N*n*), which is drawn from the research
`docs/superpowers/research/2026-11-05-communications-messaging-channels.md` ("R").

**Abbreviations:**

| Short | Path |
| --- | --- |
| `srv/` | `apps/server/internal/` |
| `mig/` | `srv/db/migrations/` |
| `cfe/` | `apps/communications/frontend/src/` |
| `pfe/` | `apps/products/frontend/src/` |
| `mfe/` | `apps/meta/frontend/src/` |

## How it lands: one stack

The owner asked for stacked PRs so that work never waits for a merge.

- **Tooling.** The stack is managed with `gh stack`, always non-interactively (`view --json`,
  `submit --auto`, `add <branch>`).
- **Merging.** The owner merges with `gh stack merge <top PR> --yes`.
- **Layers.** Each layer is one PR whose diff is its delivery alone, and its body says what it
  sits on.

| # | Branch | Delivery | Migrations claimed (main after Google's Gmail channels, latest `00129`) |
| --- | --- | --- | --- |
| 1 | `claude/communications-channel-expansion-1f26f9` | R, the spec and this plan (docs only) | — |
| 2 | `claude/communications-messenger-a-groundwork` | A: types, the slot, the sync, components, sending in parts, chat UI | `00130_communications_messaging` |
| 3 | `claude/products-images` | P: product images | `00131_products_images` |
| 4 | `claude/meta-messenger` | B: the Meta module | `00132_meta_baseline` |
| 5 | `claude/communications-messenger-c-products` | C: a product in a message, the public media route | — (C's columns are A1's `role` and P1) |

**Layer order.** P sits on A only to keep a single stack: it shares no code with A, and could
land alone. If `main` takes one of these migration numbers first, renumber inside the owning
layer's history, then run `gh stack rebase` (memory: prs-land-by-rebase).

**Layer 1 carries no `Docs-Impact` trailer.** It touches only `docs/superpowers/**`, so no
page's `sources` names it. A trailer anywhere in a merge-queue group would waive every layer's
coverage findings.

## Delivery 0 — the Meta checks (owner and agent, beside A and P)

Delivery 0 is not a layer. It gates layer 4, and N18's token expiry in layer 5.

**The owner** creates, since the agent cannot create accounts:

- a Meta developer account;
- a test app with the Messenger product;
- a test Page;
- a second Facebook account with no role on the app.

**The agent**, with the owner at the keyboard for anything that signs in:

1. **Q1, App Review.** Read Meta's current access-level documentation for `pages_messaging`.
   Then, with the app in Live mode at Standard Access, message the test Page from the account
   with no role on the app, and record whether the webhook fires and whether the Send API
   answers. Graph API Explorer and a tunnelled local webhook are enough.
2. **Q2, image caching.** Send a generic template whose `image_url` points at a tunnelled
   endpoint. Take the endpoint down. Open the conversation on a fresh device or browser after
   an hour and after a day, and record whether the image still shows.
3. **Q3, Human Agent.** Record whether the permission can be requested for a test app, and how
   long it takes.

**Output.** A short "Delivery 0 findings" section is appended to R, and the spec's §8 is
updated with the answers. On Q1 = "Advanced Access needed", **stop and ask the owner** before
layer 4: either the pilot goes through App Review, or the Vantigo-owned app moves in.

## How each delivery is run

This is the routine Inventory and Orders used.

1. **The seam, by hand, first.** In one commit, so that parallel lanes build on a tree that
   compiles:
   - the migration;
   - the OpenAPI contract, with a `summary` on every operation;
   - `make generate` and sqlc;
   - the contract types in `srv/contracts`;
   - the module skeleton and the registries.
2. **Lanes in parallel.** Each lane owns disjoint files, and never puts two agents in one Go
   package.
3. **A docs lane** (the spec's §7), English first and bokmål after, then `go generate ./...` in
   `apps/server`.
4. **An opus code review** of server and frontend, and fixes for what it finds.
5. **Gates:**
   - `mise run server:check`;
   - `mise run server:test`. On SQLSTATE 53300, rerun the failing packages with `-p 1`;
   - `mise run frontend:check`. Read the log, because the wrapper exits 0;
   - `mise run docs:check`.

   A fresh worktree needs `bun install --frozen-lockfile` first.
6. **Signed Conventional Commits.**
   - Never use `--no-gpg-sign`, `filter-branch` or amend. Add follow-up commits instead.
   - Group them as `feat(<module>)`, then `docs(<module>)`, then
     `chore(assistant): regenerate the embedded documentation`.
7. **Next layer.** `gh stack add <next branch>`, and the next delivery starts. Then
   `gh stack submit --auto --open`, watch CI, and fix red in the layer that owns it (then
   `gh stack rebase --upstack` and `gh stack push`). If the auto-mode classifier blocks a
   force-push with lease, hand the exact command to the owner.

---

## Layer 1 — the design documents

Commit R, the spec and this plan as `docs(communications): research, design and plan for
Messenger, answer options and product images`. Then `gh stack init` adopts this branch as the
bottom of the stack.

## Layer 2 — delivery A, the groundwork in Communications

### Seam (by hand)

**The migration**, `mig/00130_communications_messaging.sql`:

- `conversation_messages.components jsonb`;
- `message_attachments.role varchar(10) NOT NULL DEFAULT 'file'` with CHECK `file`/`card`;
- `communications.messenger_sync` (N3), with the key `channel_id` and the state CHECK;
- `communications.messenger_parts` (N7), with the key `(message_id, part_index)` and
  `ON DELETE CASCADE` from the message.

**The contract**, `srv/contracts/channel_messenger.go`:

- the `ChannelMessenger` interface (N2);
- `MessengerStatus`, `MessengerCapabilities` (limits and window), `MessengerEndpoint`,
  `MessengerPart`, `MessengerSent`, `MessengerEvent` (with the kinds `message`, `delivered`,
  `read` and the `echo` flag), `MessengerFile` and `MessengerProfile`;
- the errors `ErrMessengerUnavailable`, `ErrRecipientUnreachable`, `ErrReplyWindowClosed` and
  `ErrHumanAgentNotPermitted`, plus a temporary-error wrapper.

**The module wiring:**

- `module.Module.ChannelMessenger` and `Deps.ChannelMessengers` (`srv/module/module.go`,
  `compose.go`);
- Compose refuses a key that is filled in both the mailbox slot and the messenger slot.

**`openapi/communications.yaml`:**

- the `MessageComponents` and `answerTo` schemas;
- `replyWindow` on the conversation detail;
- components on `ReplyRequest`;
- the channel `type` prose names `messenger`;
- the new 422 codes: `provider_type_mismatch`, `components_unsupported`, `components_invalid`,
  `reply_window_closed`, `not_supported_on_channel`, `text_too_long` and
  `channel_cannot_start`.

`make generate` and sqlc run after these.

### Lanes

**1. Channels and dispatch** (`srv/communications/channels*.go`, `contractscalls.go`,
`queries/channels.sql`, `queries/mailbox_sync.sql`, `channel_providers.go`):

- the type gate and the address rule per type (N1);
- lookup through the slots instead of `isMailboxProvider`;
- the three default queries scoped by `type`, still under `lockDefaultChannelSlot`;
- the mailbox candidate and claim predicates gaining `c.type = 'email'`;
- `GET /channel-providers` also listing messenger providers, each with its `Endpoints`;
- creating a `messenger_sync` row with a Messenger channel.

**Tests:**

- a Messenger default leaving the e-mail default alone;
- the mailbox sync never claiming a Messenger channel;
- `provider_type_mismatch`;
- the existing concurrency tests of divergences 14 and 15, which must still pass.

**2. The messenger sync** (the new `messenger_sync.go`, `queries/messenger_sync.sql`, and the
import side of `inbound_threading.go`):

- the N3 cycle: the 2 s `Notified` tick, the 60 s fallback, the conditional-update claim;
- `Pending`, then the attachment fetch and `Profile` outside the transaction, then the one
  transaction, then `Ack`;
- threading by participant (N3a);
- watermark events;
- echoes, which are imported as outbound and ignored when their `mid` is in `messenger_parts`;
- the worker registered as `communications-messenger-sync`, and added to `module_test.go`'s
  worker list.

**Tests,** against the harness's new fake `ChannelMessenger`
(`harness_test.go`, `fake_messenger_test.go`):

- replay idempotency and out-of-order events;
- a rollback leaving a cleanup record;
- the `errCallInsideTransaction` guard;
- two syncs racing for one channel, behind the `pg_stat_activity` gate.

**3. Reply and the outbox** (`conversations_reply.go`, `conversations_create.go`,
`outbox.go`, the new `outbox_messenger.go`, `queries/outbox.sql`):

- reply on Messenger: the components checked against `Capabilities`;
- `replyFingerprint` including the components;
- the window (N6);
- refusing or ignoring the e-mail-only inputs, and the text limit;
- create refusing a Messenger channel;
- the messenger branch of the outbox:
  - the ordered parts;
  - `messenger_parts` progress, written in its own statements;
  - lease renewal between parts by `lease_id`;
  - the window checked again;
  - the error mapping onto temporary and terminal outcomes;
  - the duplicate metric for each part.

**Tests:**

- a reclaim after part 2 of 3;
- a part attempted and never sent;
- a lost lease;
- the 24 h and 7-day boundaries;
- every new 422.

**4. The frontend** (`cfe/`):

- `api/channels.ts` and `api/conversations.ts` types;
- `ChannelForm.tsx` with a type choice first and the Page picker;
- `ConversationThread.tsx` rendering by type: bubbles, inline images, chips, answers under their
  question, the carousel component (built here, fed by C), and the "Sent from Meta" mark;
- `ConversationComposer.tsx` in chat mode:
  - no subject or Cc;
  - the window banner;
  - *Add Yes / No*, *Add option* (up to 13, × to remove, a 20-character counter) and *Ask for
    e-mail*;
- the channel icon on inbox rows;
- the i18n strings in en and nb;
- Vitest for the composer's limits and the window states.

**5. Docs:**

- `U/communications.md` (en, nb);
- `Ref/communications.md`, including divergence 16, the worker, `messenger_parts`, the 422s and
  the participant orphan note;
- `A/installation.md` (en, nb), for the worker;
- `contributing/module-boundaries.md`, for the new contract slot;
- the `sources` frontmatter, which gains `00130` and the new files.

**Done when:** a conversation on the fake provider receives, threads, answers and sends in
parts in the integration tests, and the gates pass. There is no browser check yet, since there
is no real provider.

## Layer 3 — delivery P, product images

### Seam (by hand)

- `mig/00131_products_images.sql`: `products.product_images` (N13), with the partial unique
  index on `is_primary` and the CHECK on `uses`.
- `openapi/products.yaml`: the five operations of N13, with their summaries; content and upload
  are file operations.
- `srv/contracts/catalog.go`: `ProductImage`, `ProductImages` and `OpenProductImage` (N15).
- `srv/imagemeta`: `Orientation(b []byte) (int, error)`, which reads the EXIF orientation by
  walking segments, as the package already does.

### Lanes

**1. Server** (`srv/products/images.go`, `queries/images.sql`, `catalog.go`):

- the N13 normalisation pipeline: `DecodeConfig` and the 40 MP cap, orientation, decode,
  rotate, scale to 2048, re-encode, the 320 px thumbnail with `x/image/draw`;
- the storage scope `products-images`;
- the primary rules;
- the catalog calls;
- deleting a product's objects when the product or the image is deleted, via a cleanup row or
  a direct delete after commit, following how Communications' cleanup does it.

**Tests:**

- the refusals;
- orientation, using fixtures with orientations 6 and 8;
- metadata gone;
- the thumbnail's size;
- the primary rules;
- the CHECK on `uses`;
- the catalog calls.

The integration test's `filesOrRedirects` lists the upload and content operations.

**2. The frontend** (`pfe/`):

- the product page's *Images* section, with drop or pick, thumbnails, *Move left* and *Move
  right*, the star, alt text, the two usage checkboxes and delete;
- the i18n strings in en and nb;
- Vitest.

**3. Docs:**

- `U/products.md` (en, nb);
- `Ref/products.md`, including `00131` in `sources`;
- `contributing/module-boundaries.md`, for the catalog calls.

**Done when:** the gates pass, and a browser check uploads a rotated phone photo and shows it
upright with no EXIF data.

## Layer 4 — delivery B, the Meta module

**It starts only after delivery 0's Q1 answer**, and after the owner's choice if that answer
is "Advanced Access needed".

### Seam (by hand)

- `mig/00132_meta_baseline.sql`: schema `meta` with:
  - `settings`: the app id, the sealed secret, the verify token and the human-agent flag;
  - `pages`: the Page id, the name, the sealed token, the state and the last error;
  - `webhook_events`;
  - `inbound`, with the bigserial cursor, the kind, the dedup key unique per Page, the payload,
    a storage key and `acked_at`;
  - `profiles`, with the PSID, the name and `fetched_at`.
- `openapi/meta.yaml`:
  - the settings and Page operations, owner-only as the Entra and Google pages are;
  - `GET` and `POST /meta/webhook` with `x-vantigo-access: anonymous`, the POST marked as a
    hand-written handler, and their own rate-limit policy.
- The module skeleton `srv/meta`, filling `ChannelMessenger` under the key `meta`.
- **Every registry** in `AGENTS.md`'s *Adding a module* list:
  - the depguard sets in `apps/server/.golangci.yml`, including the hyphenated ones;
  - `everyModule()` in `srv/integration/assistant_test.go`;
  - `APP_ROUTE_PREFIXES` and `appRoutes`, and bump `promptVersion`;
  - `modulesEnv` in `srv/modtest/modtest.go`, since `meta` requires `communications`;
  - `moduleKeys`, `apps.ts`, the layout route, `i18n.ts`, the admin permission labels, the
    dashboard and the spotlight;
  - `sessionClients` in `apps/host/frontend/src/api/session-handlers.ts`.
- `security.PayRoutes()`: the GET and POST `/api/v1/meta/webhook` routes.

### Lanes

**1. The webhook and the inbound queue** (`srv/meta/webhook.go`, `inbound_worker.go`,
`retention.go`):

- the handshake;
- raw-body HMAC in constant time, with the 1 MiB cap, 401 and 503;
- storing the raw event;
- the splitting worker, with the dedup keys and the CDN fetch to the scope `meta-inbound`;
- `Notified`, `Pending`, `OpenAttachment` and `Ack`;
- retention at 7, 7 and 30 days.

**Tests:**

- a fixture signed as in Meta's documentation;
- the same body re-serialised failing;
- the cap;
- the database being down;
- the dedup keys;
- retention.

**2. Graph and Send** (`srv/meta/graph.go`, `send.go`, `pages.go`):

- a Graph client on `HTTPTransport`, with the netguard as Google and Entra use it;
- `Send` for text, multipart `filedata`, quick replies, `user_email` and the generic template;
- the error map in N12, and every row of it tested against an `httptest` fake;
- adding a Page: `/me`, the task check and `subscribed_apps` with the five fields;
- removing a Page;
- `Check` and `Endpoints`;
- `Profile` with the 30-day cache.

**3. Settings** (`srv/meta/settings.go`): the N10 endpoints, the refusal without a public host,
regenerating the verify token, and the status line.

**4. The frontend** (`mfe/`, and `apps/host` for Integrations):

- the *Meta* page under Integrations: the app ID and secret, the verify token and webhook URL
  each with a copy button, the Pages list with *Add Page* and *Remove*, the Human Agent
  checkbox, and the status;
- the i18n strings in en and nb.

**5. Docs:**

- the new `A/meta.md` (en, nb);
- `A/index.md` (en, nb);
- `A/installation.md` and `A/payments.md` (en, nb), for the public route;
- `U/communications.md` (en, nb), for a Messenger channel;
- the new `Ref/meta.md`;
- `contributing/module-boundaries.md`;
- `Ref/assistant.md`, if the assist targets change.

**Done when:** the gates pass, and a browser check against the test Page from delivery 0 shows
all of this working:

- connecting the Page;
- a message from the test account arriving within seconds;
- a reply with an image and Yes / No arriving;
- the tap coming back as an answer;
- a message from Business Suite showing as "Sent from Meta";
- read receipts.

## Layer 5 — delivery C, a product in a message

### Seam (by hand)

- `openapi/communications.yaml`:
  - `cards` on `ReplyRequest` with `productImageId`;
  - the 422 `product_image_unavailable`;
  - `GET` and `HEAD /communications/public-media/{token}`, anonymous.
- `security.PayRoutes()`: the media route with `getHead` and `OneSegment`.

### Lanes

**1. Server** (`conversations_reply.go`, the new `public_media.go`, `outbox_messenger.go`):

- the cards checked through `ProductImages(…, "communications")`;
- the copy before the transaction, with its cleanup record;
- the `card` attachments;
- the product id in `channel_metadata_json`;
- the media token `<id>.<exp>.<mac>`, using the Box's `MAC` under
  `communications/public-media`, with the expiry from delivery 0's Q2;
- the route: `card` attachments of outbound messages only, the headers of N18, and 404 for
  every refusal;
- the outbox's carousel part, built with the media URLs;
- the *Buy* answer resolving its product for the thread.

**Tests:** the spec's §10 list for C.

**2. The frontend** (`cfe/`):

- *Attach product*: the picker, hidden without `products:products-view` or without the
  Products module;
- the carousel preview, with cards that can be removed, the subtitle prefilled under
  `pricing-view` and editable, *Kjøp* / *Buy*, and an optional second choice;
- the "Wants to buy" answer;
- the *Create order* link, shown when Orders is present and the user may create orders. It
  carries the customer and the variant, or opens the variant picker.

  This needs Orders' new-order page to accept `customerId` and `productId` or `variantId` as
  query parameters. If it does not yet, add them in this layer, as a small `feat(orders)`
  commit with its own `U/orders.md` line in en and nb.

**3. Docs:**

- `U/communications.md` (en, nb);
- `Ref/communications.md`, for the copy and the media route;
- `A/installation.md` and `A/payments.md` (en, nb), for the media route;
- `Ref/products.md`, for the use of `communications`.

**Done when:** the gates pass, and a browser check against the test Page shows:

- a product with three images arriving as a carousel;
- *Kjøp* coming back as "Wants to buy";
- *Create order* opening with the customer and the variant filled in;
- after the image token has expired, the carousel still showing, or Q2's long expiry
  configured.

## Gotchas carried from earlier deliveries

- **OpenAPI.** Every operation needs a `summary`. File operations go in the integration test's
  `filesOrRedirects`.
- **Docs.** A docs edit needs `go generate ./...` in `apps/server`. The docs check wants
  `contributing/module-boundaries.md` for any change to `contracts/*.go`.
- **Tests.**
  - A whole-tree `go test` can exhaust the shared Postgres, so rerun single packages with
    `-p 1`.
  - Frontend tests on Node 26 have a localStorage quirk in Projects only.
- **Commits.**
  - Signed only: SSH through 1Password.
  - The pre-commit hook checks the whole tree, so commit only when every lane is clean.
  - Amending is blocked, so add follow-up commits.
  - Strip any `Docs-Impact` trailer when the docs land later in the same PR.
- **Provider calls** never run inside a transaction. `errCallInsideTransaction` is enforced;
  test it rather than assume it.
- **Webhook signatures** are checked over the raw bytes. Never decode the body before the
  check.
