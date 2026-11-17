# Invoices phase 6 — implementation plan

This plan builds phase 6 of Invoices. It works from:

- **The spec:** `docs/superpowers/specs/2026-11-17-invoices-phase6-design.md`, revision
  4. Its decisions are numbered I*n*.
- **The research:** `docs/superpowers/research/2026-11-17-invoices-competitive-gaps.md`.

This revision details **delivery A**. B to E are sketched, and each gets its own section
before it starts.

**Abbreviations:**

- `srv/` is `apps/server/internal/`;
- `inv/` is `srv/invoices/`;
- `mig/` is `srv/db/migrations/`;
- `fe/` is `apps/invoices/frontend/src/`.

## How it lands

- **One PR per delivery, against `main`.** A, B and D do not depend on each other. C
  waits for B's `amount_nok`.
- **Migrations:** each delivery claims the next number at the time it opens its PR, and
  rechecks it against `main` right before merging.
- **Order** (spec §14 Q8): A, B, C, E, D. D's code may start before C, against MPS's test
  environment. Its live send waits for Vantigo's submitter agreement with Mastercard
  Payment Services.

| Delivery | Branch | Migration (main at `a9f140c82`, latest `00192`) |
| --- | --- | --- |
| A | `claude/invoices-branding` | `00193_invoices_branding` |
| B | `claude/invoices-credit` | next free |
| C | `claude/invoices-currency` | next free |
| D | `claude/invoices-efaktura` | next free |
| E | `claude/invoices-send-all` | next free |

## Delivery A — the logo, the phone, the HTML mail

### A1. Migration `00193_invoices_branding.sql`

- `invoices.settings`:
  - `logo_object_key varchar(200)`, `logo_sha256 char(64)` and
    `logo_content_type varchar(20)`, all NULL. `ck_settings_logo` requires all three or
    none, and the content type to be `image/png` or `image/jpeg`.
  - `phone varchar(30) NOT NULL DEFAULT ''`, as the other text columns are
    (`mig/00034:18-42`).
- `invoices.invoices`:
  - `seller_logo_object_key varchar(200)` and `seller_logo_sha256 char(64)`, NULL, both
    or none.
  - `seller_phone varchar(30)`, NULL.
  - No trigger change: `refuse_issued_document_change` compares whole rows
    (`mig/00034:254-281`).
- `invoices.deliveries`:
  - `message varchar(2000) NOT NULL DEFAULT ''`.
  - Replace `refuse_delivery_change` (`mig/00035:127-140`). It now allows an update
    that sets `recipient` and `message` to `''` and changes nothing else.
  - Replace `guard_delivery_insert` (`mig/00035:156-173`). For an erased customer it
    blanks `message` as well as `recipient`.
  - The Down restores both functions as they were.
- Append the file to `inv/sqlc.yaml`'s schema list. `srv/db/schema_test.go` checks the
  list.

### A2. Server: settings and the logo

- **Queries** (`inv/queries/settings.sql`):
  - `UpdateSettings` gains `phone`.
  - New `SetSettingsLogo` and `ClearSettingsLogo`, under `LockSettings`.
  - `GetSettings` returns the new columns.
- **Handlers** (`inv/settings.go`):
  - `parseSettings` validates `phone` by Customers' rule (`srv/customers/values.go:326`).
    Copy the rule rather than import it, under rule 1. Name the field `phone`.
  - `settingsResponse` answers `phone` and `logo` (`{contentType, sha256, updatedAt}` or
    null).
- **`PUT /api/v1/invoices/settings/logo`** (multipart `file`, `invoices:manage`):
  - It follows `srv/staff/person_photo.go:178-284` and its form helpers
    (`person_photo_validation.go`):
    1. Read with a limit of 512 KiB + 1.
    2. Sniff the type, PNG or JPEG.
    3. `image.DecodeConfig`: the longer side at most 2,000 px, the shorter at least
       16 px.
    4. `imagemeta.Strip`.
    5. Compute the SHA-256 and `objectPut` at `branding/logo-{sha256}.{png|jpg}`.
    6. Under `LockSettings`, set the columns.
  - It answers 200 with the settings, and refuses with:
    - 400 `logo_unsupported_type`, `logo_too_large`, `logo_too_wide`, `logo_too_small`
      or `logo_invalid`;
    - 503 `storage_unavailable`.
  - The body limit is under the router's default of 1 MiB, so no `BodyLimits` entry is
    needed. Say so in a comment.
- **`DELETE /api/v1/invoices/settings/logo`** (`invoices:manage`) clears the columns and
  never deletes the object (I1). It answers 204, or 404 when no logo is set.
- **`GET /api/v1/invoices/settings/logo`** (`invoices:access`) returns the bytes for the
  settings page, with `ETag` set to the SHA-256 and `Cache-Control: private, max-age=0,
  must-revalidate`.
- **Events:** the module's settings change event, if it has one, names `logo_set`,
  `logo_removed` and `phone`. If it has none, add nothing.

### A3. Server: the snapshot, the PDF and the letters

- **Issue** (`inv/issue.go:672-685` `sellerSnapshot`, `inv/queries/invoices.sql:155-168`)
  copies `phone` and the logo columns. Credit notes take them the same way.
- **The preview** (`inv/pdfstore.go:632`, `copyIssueParams` `:731-745`) carries the new
  fields.
- **The model:**
  - `pdfParty` (`inv/pdf.go:131-135`) gains `phone`. `sellerLines` (`:349-366`) prints
    it after the e-mail.
  - `pdfDocument` gains `logo []byte` and `logoType`. `pdfDocumentOf` (`inv/pdfstore.go:44`)
    loads the object by the snapshot's key.
  - The loaded bytes must hash to the snapshot's SHA-256, or the render fails.
- **The layout** (`inv/pdf.go:629`, the title row):
  - With a logo, the row is the image in a column fitted to 45 × 20 mm, with the
    proportions kept (`image.NewFromBytesCol`), beside the title.
  - Without one, the layout is exactly today's.
  - Do not change `pinnedRenderSHA256` (`inv/pdf_internal_test.go:234`). A document
    without a logo or phone must render byte for byte as before; that is the test that
    nothing else moved.
  - Add a second pinned hash for a fixture with a logo.
- **Reminder letters** (`inv/reminderpdf.go:51,137,352`): the same title row and seller
  lines. The reminder goldens with no logo stay as they are.
- **`contracts.SellerProfile`** (`inv/sales_tax.go:26-29,108-120`) answers `Phone` and
  `LogoStorageKey` from the settings, and the comment changes.

### A4. Server: the HTML mail and the message

- **`inv/mailtext.go`:**
  - `mailText` gains `html`. A `coverMail` HTML template (`html/template`, embedded),
    per language, renders the same text:
    - paragraphs;
    - the pay link as a link;
    - the logo as `<img src="cid:logo" alt="{seller name}" style="max-height:60px">`
      when there is one.
  - No external URL other than the pay link, no tracking, and inline styles only.
  - Update the file comment.
- **Send** (`inv/send.go:232-420`):
  - `InvoicesSendRequest` gains `message` (optional, at most 2,000 characters, trimmed).
    It is validated as 400 `message_too_long`.
  - The message goes above the standard text in both bodies, escaped in the HTML, with
    line breaks kept as `<br>`.
  - `InsertDelivery` (`inv/queries/deliveries.sql:8-18`) stores the message.
  - `envelope` sets `HTMLBody` and adds the logo as an inline attachment (`ContentID:
    "logo"`, `Inline: true`) beside the PDF.
- **Reminders** (`inv/reminder_worker.go:317-321`): the same HTML wrapper, with no
  message.
- **Erasure:** `BlankCustomerDeliveries` (`inv/queries/customers.sql:57-64`) sets
  `message = ''` as well.
- **The deliveries' response** answers `message`.
- **The Cloudflare driver** (`srv/mail/cloudflare.go:199-209`):
  - An inline attachment goes as `disposition: "inline"` with `content_id` set to its
    `ContentID`.
  - `cloudflareAttachment` gains `ContentID string \`json:"content_id,omitempty"\``.
  - Update the driver's test, replacing the "cannot carry an inline attachment" case.
- **The SMTP renderer** already embeds inline parts (`srv/mail/outbound.go:268-285`).
  Add a test that a document mail carries `multipart/related` with the logo.

### A5. OpenAPI and generation

- `openapi/invoices.yaml`:
  - settings request and response: `phone`, `logo`;
  - the three logo operations, with multipart following `openapi/staff.yaml:11981`;
  - `InvoicesSendRequest.message`;
  - the delivery's `message`.
- Run `go generate ./...` in `apps/server` (sqlc, oapi-codegen, the embedded docs) and
  `bun run gen:client`. Commit `fe/api-schema.d.ts`. CI checks it.

### A6. Frontend

- **`fe/pages/settings.tsx`** (`SellerForm` `:229`):
  - a **Phone** input next to the e-mail (`:395`);
  - a **Logo** block above the footer:
    - the current logo as an image, from the GET, with the SHA-256 as the cache key;
    - **Upload logo** (PNG or JPEG, up to 512 KB) and **Remove logo**;
    - the refusals, shown by their codes through `lib/errors.ts`;
    - help text: *Shown at the top of the invoice, credit notes and reminder letters
      issued from now on. Documents already issued keep the logo they were issued
      with.*
- **`fe/api/settings.ts`:** `uploadInvoiceLogo(file)` and `removeInvoiceLogo()`, which
  invalidate the settings query.
- **`fe/pages/-send-dialog.tsx`** (`:165-175`):
  - a **Message** `Textarea` (optional, 2,000 characters with a counter) below the
    recipient;
  - its help text: *Shown above the standard text in the e-mail.*
- **`fe/components/deliveries-card.tsx`** shows a delivery's message, when it has one,
  collapsed.
- **i18n:** every new string in `en` and `nb` (`fe/i18n.ts`). `bun run translations:check`
  passes.
- **Tests:**
  - the logo block: upload, a refusal, remove;
  - the send dialog sends `message`.

### A7. Seed data

`apps/server/cmd/vantigo/seed.go`, `seedInvoicesReceivables` (`:1525`):

- set a phone in the settings;
- store a small generated PNG logo through the object store, and set the logo columns;
- give the issued seed invoices' snapshots (`:1660-1688`) the phone and the logo.

### A8. Documentation (spec §10)

- **`docs/src/content/docs/en/user/invoices.md` and `nb/user/invoices.md`:**
  - *The seller record and the number series*: the phone and the logo, and what is
    issued with which logo;
  - *Sending a document by e-mail* › *Sending*: the message, and that the mail shows the
    logo.
- **`en/reference/invoices.md`:**
  - the settings, invoices and deliveries rows of the model table;
  - *The PDF*: the logo's place and size, and the snapshot;
  - *Sending a document*: HTML and text, the inline logo, the message, its limit and its
    erasure;
  - the endpoints and their codes;
  - `Seller(ctx)` (`:5740-5743`): Phone and LogoStorageKey are now filled;
  - *What comes next*: phase 6 A done.
  - `sources` gains the migration.
- **`admin/` page that documents the mail drivers:** the Cloudflare driver now carries
  inline images. Find the page by `bun run tools/docs/check-coverage.ts --list` for
  `srv/mail/`.
- **`ROADMAP.md`, Invoices:** a *Phase 6* heading with A done, and B to E listed.
- Run `go generate ./...` again after the docs, since the assistant embeds them.
- **The public site** (`../vantigo-landing`, `web/src/apps/invoices.ts`): one line under
  what the app does, on the logo on invoices and in mail. Make it a PR there, linked
  from this one. If the repository is not checked out, say so in the PR.

### A9. Assistant

- `fe/assist-targets.json`: `invoices.settings.logo` and `invoices.send.message`, with
  `en` and `nb` labels.
- Bump `promptVersion` in `srv/assistant/prompt.go:15`.

### A10. The gates

- `mise run server:check`
- `mise run server:test`, or the invoices, mail and accounting packages with `-p 1` if
  the shared database runs out of clients
- `mise run frontend:check`: read its log, since its wrapper exits 0 anyway
- `mise run docs:check`
- Commits follow Conventional Commits, `feat(invoices): …`, and are SSH-signed.

### A11. Review

An independent review of the diff before the PR leaves draft. It checks, in particular:

- the two trigger replacements;
- the SHA check on the loaded logo;
- HTML escaping of the message;
- the byte-identical PDF without a logo.

## Deliveries B to E

Each gets its own section here before it starts, written from the spec's decisions:

- **B:** I8–I17a;
- **C:** I17–I28;
- **D:** I29–I35;
- **E:** I36–I38.
