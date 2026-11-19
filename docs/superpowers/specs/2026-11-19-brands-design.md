# Brands — design

Date: 2026-11-19. Status: design, ahead of the plan
(`docs/superpowers/plans/2026-11-19-brands.md`). Research:
`docs/superpowers/research/2026-11-18-dark-theme-and-brands.md` (§3, and the owner's
binding decisions in §6). It builds on the dark-theme foundation (PR #383, delivery
D-A): the token layer in `packages/frontend-shell/src/theme.css` and `theme.ts`, and
the pre-paint snippet in `apps/server/internal/web/index.go`.

A company that trades under several brands wants each office, department or group to
work in its own: its name, its colours, its logos, in light and in dark. Brands are
**workspace configuration** owned by the tenant, edited at `/workspace/theme` by an
Owner or whoever holds `identity:brands`. A brand is assigned to people by ordered
rules; a person in several brands may switch. Before sign-in, the page fades into the
brand that matches the e-mail domain being typed. A brand can carry dated **campaign**
overlays, and it can be put on a customer's documents.

Rules are numbered **B1, B2, …** so that implementers and reviewers can cite them.
`srv/` abbreviates `apps/server/internal/`, `host/` `apps/host/frontend/src/`, `shell/`
`packages/frontend-shell/src/`, `mig/` `srv/db/migrations/`; `U/`, `A/`, `R/` the user,
administration and reference sections of `docs/src/content/docs/en/` (and `nb/` where
the section is bilingual).

## Readings

What exists, checked against `origin/feat/dark-theme-foundation` on 2026-11-19:

- **Token layer (D-A).** `shell/theme.css` defines `--vantigo-body-bg`, `-surface`,
  `-text`, `-text-dimmed`, `-border`, `-link`, `-subtle-bg` for `:root` and
  `:root[data-mantine-color-scheme="dark"]`. `shell/theme.ts` has `vantigoTheme`
  (a `createTheme` value: `colors: { vantigo, dark }`, `primaryColor: "vantigo"`,
  `primaryShade: { light: 6, dark: 6 }`, `defaultRadius: "md"`), `vantigoTokens`, and the
  storage key `vantigo-color-scheme`. Light link is `vantigo-8`, dark link `vantigo-3`,
  light subtle background `vantigo-0`. One `MantineProvider`, in `host/main.tsx`.
- **Pre-paint.** `colorSchemeScript` in `srv/web/index.go` is appended to the one inline
  script `runtimeConfigScript` returns; the CSP hash is computed from that exact string
  (`NewIndex`). Anything this design adds to the snippet goes in the same string.
- **Env branding.** `config.Branding` (`srv/config/config.go:42`, parsed at `:1772`)
  holds `APP_TITLE`, `APP_LOGO_URL` and `APP_SUPPORT_*`; `NewIndex` injects them
  into `window.__VANTIGO_APP__` and the `<title>` at startup. The index is built once
  per process, so it cannot carry a value that lives in the database.
- **Identity.** `identity.access_groups` (`mig/00002_identity_baseline.sql:62`: uuid id,
  `display_name`, `source` local or scim, `is_active`) with
  `access_group_memberships (group_id, user_id, …, membership_override)` — a person is in
  a group only when the membership counts (the same rule `groups.go` and the role
  mapping use). `identity.users` has `email` and `normalized_email` (unique), and since
  D-A `color_scheme` (`mig/00205_identity_color_scheme.sql`). Permissions are declared in
  `Module(a)` (`srv/identity/module.go`), grammar `^[a-z]+:[a-z-]+$`
  (`srv/contracts/permission.go`); an Owner holds every key
  (`identity/access_test.go:439`). Rate limits are `ratelimit.Policy` values in the
  module's `limits` map (`module.go:67`). The authorization audit trail is `writeAudit`
  (`srv/identity/audit.go`), written in the changing transaction.
- **Staff.** `staff.legal_entities` (bigint id, `name`, `org_number`, `archived_at`),
  `staff.business_units (legal_entity_id)`, `staff.employments (business_unit_id,
  start_date, end_date)`, and `staff.identity_links (person_id, provider, external_id)`
  with provider `'identity'` and `external_id` the identity user id
  (`mig/00042_staff_foundation.sql`). Identity cannot import Staff (depguard) or read
  its schema, so the legal entity target needs a contract (B9).
- **Customers.** `customers.customer_groups` and `customers.customers.group_id`
  (`mig/00027_customers_groups.sql`).
- **Invoices logo.** `srv/invoices/logo.go`: a 512 KiB cap, `http.DetectContentType` and
  `image.DecodeConfig` must agree on PNG or JPEG, at most 2000 px on the longer side and
  at least 16 px on the shorter, the file re-encoded to strip metadata, stored
  content-addressed as `branding/logo-{sha256}.{png|jpg}` in the module's storage scope
  and never deleted. An issued invoice snapshots `seller_logo_object_key` and
  `seller_logo_sha256` (`mig/00193_invoices_branding.sql`). `contracts.SellerProfile`
  (`srv/contracts/sales_tax.go`) carries `LogoStorageKey` to Orders.
- **Live.** `srv/live` is a platform package; a module declares `Module.LiveTopics`
  (Customers and Calendar do). Topic grammar `<module>.<kind>[:<key>]`
  (`live/topic.go`), so `identity.brand` is valid. Events carry identifiers only.
  Identity has no topics yet.
- **Where the code differs from the research note.** Recorded here and in the hand-off:
  migrations 00204 and 00205 are taken (the note said 00203; 00203 is a gap, probably a
  stack in flight), so brand migrations are numbered at merge time (B5);
  `communications/public_media.go` is a **signed, expiring** route, not a
  content-addressed one, so it is a model for an anonymous media handler's shape only
  (B15); `config.Branding` parses a logo **URL**, not a file, so the import must fetch it
  (B30).

## Decisions

### What a brand is

**B1 — A brand is a name, a title, a few colour inputs, logos and support
contacts. It is never free CSS.**

| Part | Content |
| --- | --- |
| `name` | 1–100 characters, unique case-insensitively. What the admin and the switcher show. |
| `title` | 1–60 characters. The product name in the tab, the shell header and the sign-in page. Defaults to `name`. |
| Colour inputs | `primary`: one `#rrggbb`. `radius`: `xs`, `sm`, `md`, `lg` or `xl` (Mantine's `defaultRadius`). Nothing else is an input. |
| Generated values | The 10-shade scale (B3) and the tokens (B2), computed from the inputs, in both schemes, and stored with them. |
| Logos | `mark_light` (for light backgrounds), `mark_dark` (for dark backgrounds), `icon` (square: favicon, collapsed navbar) and `wordmark` (optional, wide: sign-in page and documents). B6. |
| Support | `support_email`, `support_phone`, `support_url`, each optional (today's `APP_SUPPORT_*`). |

Free CSS is refused: it cannot be checked for contrast, it breaks on a Mantine upgrade
and it is an injection surface. An accent colour and tinted neutrals are left out on
purpose: no component consumes an accent today, and the dark neutrals were tuned by eye
in D-A. Both are future inputs (Non-goals).

**B2 — A brand customises the existing tokens, and only these.** For each scheme a
brand yields: the ten `vantigo` shades (`--mantine-color-vantigo-0…9`), `--vantigo-link`
(light: shade 8, dark: shade 3), `--vantigo-subtle-bg` (light: shade 0; dark: Mantine's
`vantigo-light`, as in D-A) and the radius. Body, surface, text, dimmed text and border
stay Vantigo's, in both schemes. `primaryShade` stays `{ light: 6, dark: 6 }`. A brand
therefore renders in light and in dark by construction; there is no brand without a
dark rendering.

**B3 — The scale is generated in OKLCH, from one hex, by a fixed algorithm.** The
algorithm is a rule of the design because it must be testable and identical on the
server (Go, the truth, which validates and stores) and in the editor's live preview
(TypeScript, a port):

1. Convert the input to OKLCH `(L6, C6, h)`. Shade 6 **is** the input, exactly.
2. Accept the input only if `0.40 ≤ L6 ≤ 0.70` (otherwise `422 brand_primary_range`: too
   light or too dark to be a primary). White text on the filled button needs this
   anyway (B4).
3. Lightness. For `i < 6`: `L_i = L6 + (0.975 − L6) · t_i` with
   `t = [1.00, 0.86, 0.68, 0.50, 0.32, 0.16]`. For `i > 6`:
   `L_i = L6 − (L6 − 0.28) · u_i` with `u = [0.33, 0.66, 1.00]` for `i = 7, 8, 9`.
   Lightness is strictly decreasing from shade 0 to 9.
4. Chroma. `C_i = C6 · k_i` with
   `k = [0.12, 0.25, 0.45, 0.68, 0.88, 1.00, 1.00, 0.95, 0.85, 0.72]`; hue `h` is
   constant.
5. Gamut. If `(L_i, C_i, h)` is outside sRGB, reduce `C_i` by bisection (to a tolerance
   of 0.001) until it is inside. Never clip channels.
6. Output `#rrggbb`, each channel rounded to the nearest integer.

Tests (B-A): shade 6 equals the input for a table of inputs; lightness strictly
decreases; hue stays within 2° of the input except for near-grey inputs (`C6 < 0.02`);
every shade is in gamut; and a **golden file**, `testdata/brand-scale.json`, holds the
scale of ten inputs including Vantigo's indigo `#4f46e5`. The Go and TypeScript
implementations are each tested against that one file, so they cannot drift. The
constants are tunable only in one place per language and only together with the golden
file.

**B4 — Contrast is validated, in both schemes, and enforced at publish.** WCAG 2.x
relative-luminance contrast, AA (4.5 : 1 for text):

| Pair | Light | Dark |
| --- | --- | --- |
| White text on the filled primary | `#ffffff` on shade 6 | `#ffffff` on shade 6 |
| Links on the page | shade 8 on `#f7f8fc` | shade 3 on `#15182a` |

(The page backgrounds are D-A's values, read from the token layer by the test, not copied
into it.) A draft may be saved failing; the editor shows each pair with its ratio and a
pass or fail. **Publish is refused** with `422 brand_contrast` naming the failing pairs
and, for each, the nearest primary (same hue and chroma, lightness moved) that passes, so
the editor can offer it with one click. The refusal is also in the server because the API
can publish without the editor. Campaign tokens (B22) are checked the same way.

**B5 — Storage is identity's.** Brands are workspace configuration, like
`identity.sso_settings`, and a separate module would cost every registry in AGENTS.md.
The tables (migrations numbered **at merge time**, after main's latest: the next free
number after 00205 today, and parallel stacks have collided before):

- `identity.brands`: `id uuid`, `name`, `title`, `is_default boolean` (a unique partial
  index allows one), `state` (`draft` | `published` | `archived`), `published jsonb`
  (the live version: inputs, generated tokens, logos, support — shape validated by a Go
  type, `version int`), `draft jsonb` (the pending version, null when none),
  `revision int` for optimistic concurrency (`If-Match`, as other identity resources
  do), timestamps.
- `identity.brand_rules (id, brand_id, position, kind, target_ref, …)`: B8.
- `identity.brand_logos (sha256 char(64) PK, content_type, object_key, created_at)`: the
  asset registry; B6.
- `identity.brand_campaigns`: B22.
- `identity.users.preferred_brand_id uuid NULL REFERENCES identity.brands ON DELETE SET
  NULL`: the personal choice, B12.

`jsonb` holds the version because a published version, a draft and a campaign overlay
are the same shape, and a brand is read whole, never queried by a token. The default
brand always exists: the first mount that finds no row creates it from the built-in
Vantigo values (B30), so resolution never has "no brand".

**B6 — Logo assets reuse the invoices pipeline and are content-addressed.** The
validation (type sniff and `DecodeConfig` agree; PNG or JPEG only, never SVG; 512 KiB;
16–2000 px; re-encoded to strip metadata) is the invoices logo's, **extracted into
`srv/storage`-adjacent shared code** rather than copied: B-A moves `judgeLogo` and the
re-encoding into a small shared package that Invoices and Identity both import, with
Invoices' tests moving with it and passing unchanged. Objects are stored in identity's
storage scope as `brands/logo-{sha256}.{png|jpg}`, written before the transaction,
never deleted (a document may snapshot one, B27). A brand's slot names a `sha256` that
exists in `identity.brand_logos`. Each slot has a size hint checked at upload: `icon`
must be square within 5 %; the marks and the wordmark may be any shape. Without object
storage configured, brands work without logos and the upload controls say why.

### Who sees which brand

**B7 — A person sees one brand: their own choice if they have one and may still have
it, otherwise the first rule that matches, otherwise the default.** Resolution is
computed on every request from current data; it is never stored (employment terms and
group memberships change).

**B8 — Assignment is an ordered list of rules; the first match wins.**
`brand_rules.kind` is one of:

| Kind | `target_ref` | Matches a person when |
| --- | --- | --- |
| `domain` | a lower-case, punycode-normalised domain, e.g. `acme.no` | the domain of the user's `normalized_email` equals it exactly (no wildcards, no subdomains) |
| `legal_entity` | a Staff legal entity id (text, because it is a bigint in another schema) | the person has an employment in force today under a business unit of that entity (B9). Only offered while Staff is enabled. |
| `access_group` | an `identity.access_groups` id | the user is a member of the group, the membership counting as it does for role mapping, and the group is active |

The list's order is the admin's, set by dragging; a new rule is inserted at the
position its kind suggests (domains, then legal entities, then groups — the owner's
order), and the admin may move it anywhere. A domain is attached to **at most one**
brand (a unique index on the domain), which is what makes the sign-in lookup (B16)
unambiguous. A `(kind, target_ref)` pair appears once in the list. Saving the list is
one `PUT` of the whole ordered list with the list's `revision`; no two admins interleave
half-edits. A rule whose target no longer exists (an archived entity, a deleted group)
is kept, matches no one, and is flagged in the editor. Rules never point at an
`archived` brand. A user of kind other than person — an API-key integration, an agent, a
station — resolves to the default brand. A person in no rule gets the default.

**B9 — Staff is reached through a contract, never imported.** A new single-provider,
read-only, in-process slot `contracts.BrandTargets`, served by Staff
(`Module.BrandTargets`), wired by Compose into identity's `Deps`; nil when Staff is
disabled, which identity reads as "no legal entity targets", never as "the person has no
entity". Methods: `LegalEntities(ctx)` (id and name, archived marked, for the picker and
for flagging stale rules) and `LegalEntitiesOf(ctx, userIDs []uuid.UUID)` (for each user
id, the legal entity ids of the employments in force today, through
`staff.identity_links` where `provider = 'identity'`, `employments` and
`business_units`). It performs no authorization (consumer-gated, like `Timesheets`) and
fails rather than under-reports. With Staff off, existing legal-entity rules stay in the
list, inert, and the editor says why.

**B10 — Resolution is one function.** `Resolve(user) → {brand, source}` with `source`
one of `choice`, `rule:<id>`, `default`. It is used by `GET /brand`, by the "who sees
what" lookup and by documents' defaults where a user is needed. Its candidate set (B12)
is computed in the same pass.

**B11 — Who sees what.** `GET /brands/resolution?userId=` (B33) answers, for a person,
the resolved brand, the source (the rule's position and kind, or "personal choice" or
"default"), the candidate set and the person's data it used: their e-mail domain, their
groups and their legal entities. It is the answer to "why am I in Brand B", and it needs
`identity:brands`.

**B12 — A person in several brands may switch.** The **candidates** of a person are the
brands of *every* rule that matches them, plus the default brand. If there is more than
one candidate, the account menu shows a *Brand* switcher listing them. The choice is
stored per user (`preferred_brand_id`) and **beats all rules** while it is a candidate;
when rules change so that it no longer is, resolution ignores it (and `GET /brand` says
so, so the switcher can show the reset). Choosing "Automatic" clears it. A person with
one candidate sees no switcher. The choice is per user, not per device.

### Resolution and delivery to the browser

**B13 — `GET /api/v1/identity/brand` (session) returns the resolved brand.** The body:
`brandId`, `version` (the published version, plus the campaign id when one applies —
the cache key), `name`, `title`, `tokens` for light and dark, `logos` as URLs (B15),
`support`, `source` (B7), `candidates` (id and name), `validUntil` (when a campaign
starts or ends, B24, so the client refetches then), and `choiceIgnored`. It is
`x-vantigo-access: session`, `x-vantigo-api: never` (a browser concern). A holder of
`identity:brands` may add `?preview={brandId}` to get that brand's **draft**, for B20.

**B14 — The runtime theme is a function of the brand.** `vantigoTheme` becomes
`createVantigoTheme(brand)`, which fills `colors.vantigo` from the brand's shades and
sets `defaultRadius`; the provider in `host/main.tsx` receives it from a `BrandProvider`
that holds the resolved brand and re-renders on change. The theme with no brand is the
Vantigo default brand exactly, so nothing changes for an installation that never edits
its brand. A `useBrand()` hook gives components the title, logos and support. The shell
chooses `mark_dark` or `mark_light` by the colour scheme, and the collapsed navbar uses
`icon`. A brand without a dark mark uses its light mark on a surface chip in dark.

**B15 — Logo files load without a session.** `GET
/api/v1/identity/brand-assets/{sha256}.{png|jpg}` is `x-vantigo-access: anonymous`,
served `immutable` with a long `max-age`, `nosniff`, rate-limited per address, and
answers **only for an asset a published brand or campaign names** (otherwise 404, so the
route is not a way to read an unpublished draft's logo; a draft's logo is read through
the authenticated preview, which streams it behind `identity:brands`). The signed,
expiring `communications/public_media.go` is a model for the handler's shape, not for
its addressing. If the public host filters by path, the route is added there in B-E
only when the pay page shows a brand logo.

**B16 — The first paint is right for a returning visitor.** The pre-paint snippet in
`runtimeConfigScript` is extended (it stays one script, so the CSP hash stays correct by
construction and `TestNewIndex_ScriptHashCoversExactlyTheInjectedScript` keeps it
honest). It reads `localStorage["vantigo-brand"]` — `{version, title, tokens}` mirrored
by the client after every `GET /brand` — and, after setting the colour scheme, writes the
brand's shade variables and `--vantigo-link`/`--vantigo-subtle-bg` for the scheme it
chose onto `document.documentElement.style` through `setProperty` (the CSSOM, not a
`style` attribute, so the CSP is untouched), and sets `document.title`. It is wrapped in
try/catch, as the scheme part is. When the client has loaded `/brand`, it removes the
inline overrides and lets the Mantine theme own the variables (one source of truth). A
first visit paints the default brand and repaints in at most one request. The Vite dev
server has no injected script; dev uses a fallback in `main.tsx`. A browser check in B-A
confirms the override reaches Mantine's derived variables
(`--mantine-primary-color-filled` and friends).

**B17 — The index no longer carries branding.** `window.__VANTIGO_APP__` keeps
`basePath` and `modules`; `title`, `logoUrl` and `support` are no longer injected (the
index cannot see the database, and a startup value would go stale). The static
`<title>` is `Vantigo`; the snippet and the client set the real one. The shell reads
title, logos and support from `useBrand()`.

**B18 — Open sessions repaint live.** Identity declares one topic, `identity.brand`
(no key), authorised for any signed-in person (the event carries no data). Publish, a
default change, a rule-list change and a campaign change `live.Notify` it **in the
writing transaction**; a session that hears `brand.changed` refetches `GET /brand`
(which is checked, so a person never receives a brand they do not resolve to). Time-based
changes (a campaign starting) need no notification: `validUntil` (B13) tells the client
when to refetch. A person's own switch repaints their tabs through the same event, sent
to the topic with their user id as the event's identifier, which other tabs ignore unless
it is theirs.

**B19 — Favicon and tab title.** The client sets `<link rel="icon">` to the brand's
`icon` (the Vantigo icon, with D-A's dark variant, when none) and `document.title` to
`<page> — <brand.title>`, the form the shell already composes with the instance title.
One favicon per brand: the editor previews it on a light and a dark tab strip so the admin
can see it fail.

### The editor and the screen

**B20 — Draft, "preview as me", publish.** Editing a published brand writes `draft`;
the published version is untouched until `POST /brands/{id}/publish`. "Preview as me"
stores the brand id in `sessionStorage` and the shell adds `?preview=` to its `GET
/brand`; it shows a banner (*You are previewing Brand X — stop*), is visible to that
admin only, and never touches the server's state. Publish checks B4, bumps `version`,
audits (B34) and notifies (B18). A brand never published is `draft`; it matches no rule
until published. "Discard draft" drops the pending version.

**B21 — The `/workspace/theme` screen** (nb: *Merkevare*; route
`/workspace/theme`, `module: identity`, `requiredPermissions: ["identity:brands"]`,
`ownerOnly` false) is a Workspace admin item beside *Roles & access*.

- **Brands list.** Name, title, swatch, the default marked, the number of rules, state
  (draft, published, archived). Create, duplicate, make default, archive.
- **Editor**, one brand at a time: name and title; the primary colour (picker and hex),
  the radius; the four logo slots with the upload errors of B6; support contacts; the
  **live preview** in both schemes side by side (a mini shell with the logo, buttons, a
  table, a badge, a link; the favicon on light and dark tab strips); the **contrast
  check** of B4 with the one-click fix; the scale as ten swatches. Save draft, Publish,
  Discard draft, Preview as me.
- **Rules.** The ordered list, drag to reorder, add rule (kind, then the target
  through a picker: a domain field, the legal entities from B9, the access groups), the
  stale-target flags (B8), and the **who sees what** lookup (B11) — a person picker, the
  resolved brand and why.
- **Campaigns** of the open brand (B22–B24), a list with a calendar-style summary.
- **Export and import** of a brand as JSON (inputs and support only, never logos'
  bytes), for moving between a test and a production instance.

An archived brand cannot be edited, assigned or chosen; it can be restored. A brand can
be **deleted** only if it was never published; otherwise it is archived, because
documents may name it (B27).

### Campaigns

**B22 — A campaign is a dated overlay on one brand.** `identity.brand_campaigns`: `id`,
`brand_id`, `name` (Pride, Christmas, …), `starts_on` and `ends_on` (dates, inclusive,
in the workspace's business time zone), `yearly boolean` (repeats every year on the same
month and day; a range crossing New Year is allowed), `overlay jsonb` (any of: a
different `primary`, different logo slots, a different `title`), `affects_email boolean`
(default false), `enabled boolean`. The overlay is the **same shape as the brand's
version with every field optional**; a field it omits is the brand's. Overlay tokens go
through B3 and B4, so a campaign is as readable as a brand. Campaigns do not change the
radius or the support contacts.

**B23 — At most one campaign is active per brand at a time.** Overlapping date ranges
(allowing for `yearly`) on one brand are refused at save (`409 campaign_overlap`),
because "which wins" is a question the admin should not have to answer in a dispute.
The resolved brand (B13) is the brand with its active campaign's overlay folded in, and
the response's `version` reflects it.

**B24 — What a campaign changes, and what never.** It changes the **workspace look**
(the shell, the sign-in page for the brand's domains, the favicon) and, **only when
`affects_email` is ticked**, the brand mark in e-mails the system sends in that brand
(when system e-mail carries one: it is plain text today, so the flag is stored and has no
effect until HTML system mail exists — said in the admin guide). It **never** changes an
issued invoice, a credit note, a reminder letter or any other legal document, which
carry the plain brand (B27, B28). `validUntil` (B13) is the next boundary of any
campaign of the person's brand, so the client repaints at midnight business time with no
worker.

### Sign-in

**B25 — Sign-in is branded by e-mail domain, with no leak.** `GET
/api/v1/identity/brand/public?domain=acme.no` is `x-vantigo-access: anonymous`,
`x-vantigo-api: never`:

- **Keyed on the domain only.** The client takes what follows the `@` of the e-mail
  field. The request never carries the local part, and the server never sees it, so it
  cannot answer differently for an address that has an account. It answers the same for
  every address on the domain.
- **Only attached domains.** If the domain is attached to a brand by a `domain` rule
  (B8) that is published, the answer is that brand's public look (name, title, tokens,
  logo URLs, the campaign overlay in force); otherwise it is the **default brand** with
  `matched: false`. Without `domain` it is the default brand. Archived or draft brands
  are never answered.
- **Rate-limited** like the other anonymous identity endpoints: a per-address policy
  (30 per minute) and a global ceiling, with `Retry-After`; over the limit, the client
  keeps the brand it has.
- **Cacheable**: `Cache-Control: public, max-age=300`, normalised domain in the key.
- **Constant work**: one indexed read; the answer for a matched and an unmatched domain
  differ only in content, not in a measurable path.

**B26 — The page fades into the brand as the e-mail is typed.** The sign-in page (and
*forgot password*, *accept invitation*, *reset password*) starts in the last-seen brand
or the default. When the e-mail field has a valid-looking domain and is **blurred, or has
been still for 400 ms**, the client calls B25 once per distinct domain and, on
`matched: true`, cross-fades the colours, the title and the wordmark over 250 ms
(respecting `prefers-reduced-motion`: no fade, an instant change). Editing the domain
back to an unattached one fades to the default. The fade is cosmetic; it never gates
sign-in. SSO buttons are unaffected. **Privacy**, stated in `A/branding.md`: a brand's
name, colours, logos and title become readable by anyone who types a domain an admin
attached — they are public brands, and an admin must not attach a domain whose brand is
confidential; the lookup reveals that *a domain is attached*, never whether any person
has an account.

### Brands on documents

**B27 — A document carries a brand and snapshots it at issue.** Invoices and Orders
(sales quotes and orders) gain `brand_id uuid` (no foreign key: another module's table,
checked through the contract of B29) and, on an issued invoice, the snapshot
`brand_name varchar(100)`, `brand_logo_object_key`, `brand_logo_sha256` beside the
existing `seller_logo_*`. At issue the document's logo is the brand's `mark_light`
(a document is printed on white; a brand without one falls back to the seller's logo
setting) copied by Invoices into **its own** storage scope under
`branding/logo-{sha256}` — the pipeline it has today — through `contracts.Brands`
(B29), so the PDF, reminder and e-mail code, `seller_logo_*` immutability and the
"never deleted" rule are untouched. `invoices.refuse_issued_document_change` already
compares the whole row, so the snapshot is immutable once issued. Changing, archiving
or campaigning a brand later never changes an issued document. **No campaign overlay is
ever applied to a document (B24).**

**B28 — Which brand a document gets.** In order: the person's explicit choice on the
document (the switch in the editor of an invoice or quote; a draft may change it freely,
an issued document never); else the **customer's default brand**; else the default of the
**customer's group**; else the **default brand**. A brand archived since is skipped (the
next in order applies, and the editor says so). Customers adds `brand_id` to
`customers.customers` and to `customers.customer_groups`, editable by whoever can edit
the customer, shown on the customer and group pages and offered through a brand picker
fed by `contracts.Brands`. Orders passes its document's brand to Invoices when an order
is invoiced, so an order's invoice starts in the order's brand.

**B29 — `contracts.Brands`.** A single-provider, read-only, in-process slot served by
Identity (`Module.Brands`), wired into every module's `Deps`; always present while
identity is (identity is mandatory). Methods: `List(ctx)` (id, name, state, default),
`Get(ctx, id)` (name, title, tokens, logo slots), `LogoBytes(ctx, sha256)` (a published
or archived brand's logo, for the copy at issue). It performs no authorization: a
consumer is gated on the permission to edit its own record. It never returns a draft.

**B29a — Seller identity stays single in this phase** (explicit non-goal). A brand
changes a document's **look and name** (logo, brand name, title) and nothing about who
the seller is: legal name, organisation number, VAT registration, address, bank account,
IBAN and the footer remain `invoices.settings`, one row. A company that trades under
several brands from **one** legal entity is served; one that issues from **several legal
entities** (separate organisation numbers and bank accounts per brand) is not — that is
multi-seller Invoices, Orders and Accounting, a phase of its own (Non-goals, Future
work). The customer pages and the editor's brand switch say so.

### Migration of the environment branding

**B30 — `APP_TITLE`, `APP_LOGO_URL` and `APP_SUPPORT_*` become a one-time import into
the default brand.** The tiers rule (`docs/superpowers/specs/2026-10-31-integrations-and-configuration-tiers-design.md`
T1) allows a setting to live in one tier only; the precedent is `OIDC_*` →
`identity.sso_settings` (`srv/identity/sso_settings.go`). At the first mount that finds
no default brand row: the default brand is created from the built-in Vantigo values and
from whichever of those variables are set (title, support contacts). A logo URL is
**fetched once** through the guarded outbound client (`Deps.HTTPTransport`: public
addresses only, host pinned, no redirects, size-bounded, then B6's validation), stored as
`mark_light`; a failure (unreachable, not an image, no object storage) imports the rest,
logs the reason and shows a notice in the editor (*APP_LOGO_URL could not be imported:
upload the logo*). The variables are **ignored since**, with a boot-time log line when
they are still set; `config.Branding` is deleted from `config` one release after the
import has shipped, and `deploy/compose/vantigo.env.example`, `A/configuration.md` and
`A/installation.md` are updated. The default brand is published at import.

### Permission, audit, API

**B31 — A new delegable permission, `identity:brands`.** `Key: "identity:brands"`,
`Display: "Manage brands"`, `Description: "Create and edit brands, their logos, assignment
rules and campaigns."`, `Category: "Administration"`, `Sensitive: false`,
`Delegable: true`. An Owner holds it implicitly. It is registered in the admin permission
labels (host), in `A/authentication.md`'s permission list and in the roles catalog. It
does not grant the sign-in lookup or `GET /brand`, which are open to everyone for what
they serve.

**B32 — Who may do what.**

| Action | Who |
| --- | --- |
| Read one's own resolved brand, list and choose among one's candidates | any signed-in person |
| Read a brand's public look, with or without a domain | anyone (B25) |
| Read, create, edit, publish, archive brands, upload logos, edit rules and campaigns, the lookup | `identity:brands` (Owners hold it) |
| Delete a never-published brand | `identity:brands` |

**B33 — The API.** Under `/api/v1/identity/`, in `openapi/identity.yaml` (the
reference regenerates). `x-vantigo-api` is `never` on the browser-only operations
(`brand`, `brand/public`, `brand-assets`) and the default on the rest, so integrators
can manage brands.

| Operation | Path | `x-vantigo-access` |
| --- | --- | --- |
| Resolved brand for me | `GET /brand` | `session` |
| Choose a brand | `PUT /account/brand` `{brandId \| null}` | `policy:ActiveAccount` |
| Public look | `GET /brand/public?domain=` | `anonymous` |
| Logo asset | `GET /brand-assets/{file}` | `anonymous` |
| List / create | `GET`, `POST /brands` | `permission:identity:brands` |
| Read / update draft / delete | `GET`, `PUT`, `DELETE /brands/{id}` | `permission:identity:brands` |
| Publish / discard draft / archive / restore / make default | `POST /brands/{id}/publish`, `/discard-draft`, `/archive`, `/restore`, `/make-default` | `permission:identity:brands` |
| Set / remove a logo | `PUT`, `DELETE /brands/{id}/logos/{slot}` (multipart `file`) | `permission:identity:brands` |
| Campaigns | `GET`, `POST /brands/{id}/campaigns`; `PUT`, `DELETE /brands/{id}/campaigns/{cid}` | `permission:identity:brands` |
| Rules | `GET`, `PUT /brand-rules` (the whole ordered list, `If-Match`) | `permission:identity:brands` |
| Who sees what | `GET /brands/resolution?userId=` | `permission:identity:brands` |
| Targets for pickers | `GET /brand-rule-targets` (groups, legal entities if Staff is on) | `permission:identity:brands` |

Errors follow identity's problem shape: `brand_contrast`, `brand_primary_range`,
`brand_in_use` (archiving the default, deleting a published brand), `campaign_overlap`,
`rule_duplicate`, `domain_taken`, `logo_unsupported_type`, `logo_too_large`,
`logo_too_wide`, `logo_too_small`, `logo_invalid`, `logo_not_square`, `stale_revision`.
Anonymous routes carry the router's rate limits from `limits`; the logo upload gets the
invoices upload's body bound (it stays under the router's 1 MiB default).

**B34 — Audit.** Each write goes through identity's `writeAudit`, in the changing
transaction: `brand.created`, `.updated`, `.published`, `.archived`, `.restored`,
`.deleted`, `.default_changed`, `.logo_set`, `.logo_removed`, `brand_rules.changed`
(before and after, as ordered ids), `brand_campaign.created|updated|deleted`. Details
carry names, versions and kinds, never the logo bytes. A person's own choice of brand is
not audited (it is personal preference, like the colour scheme).

### Not in this phase

**B35 — Non-goals.**

- **Per-brand domains** (`app.brand-a.no`): the owner said no. Everyone uses one domain;
  the sign-in fade (B25) is the substitute.
- **A "Powered by Vantigo" mark**: dropped by the owner.
- **Branding the docs site and the API reference**: they stay Vantigo's. The admin page
  says so.
- **Multi-seller** (B29a): a brand never changes the seller's legal identity.
- **Free CSS**, **SVG logos** (script vector), an **accent colour**, **tinted
  neutrals**, per-brand dark palettes.
- **HTML system e-mail** with a brand mark (system mail is plain text; `affects_email`
  waits for it).
- **Username rules**: sign-in is by e-mail address, so the domain rule covers the owner's
  "(or username)".
- **The Assistant's prompt** keeps naming the product; branding it is a separate choice.

Future work: multi-seller documents keyed on the brand; HTML system e-mail; SSO
home-realm discovery on the same domain table; an accent colour; staff groups, departments
and offices as rule targets (the research listed them; the owner chose groups, legal
entities and domains first).

## Documentation and the public site

The change is customer-visible and operator-visible: its pages are in the plan, per
delivery, in both languages. The landing site claim (platform-level, written from the
guide) is delivery B-L.

## Open questions

Only those the owner must answer; everything else above is decided, with its reason.

1. **A rule on a group that SCIM provisions from the IdP, together with the personal
   choice (B12).** A person may switch among their candidates. Should an Owner be able
   to **lock** a rule (people matching it cannot switch away), for a company where a
   brand is a compliance matter? Default if unanswered: no lock.
2. **The brand on an invoice when the customer's default brand is archived (B28).** The
   design falls back to the next default. Should the editor instead **block issuing**
   until the author picks a brand? Default if unanswered: fall back, with a notice.
3. **Brand name on documents.** Should a document print the **brand name** (e.g. under
   the logo) beside the seller's legal name? Default if unanswered: logo only, and the
   brand name is stored in the snapshot for later use.
