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
- **Outbound HTTP and the CSP.** `Deps.HTTPTransport` is nil in production (the
  `cmd/vantigo` tests say so), so it is not a guard. Identity builds its own guarded
  transport with `netguard.DialContext(resolver, dial)` (`srv/identity/webhooks_client.go:105`,
  no proxy, no redirects followed by its client). The CSP is `style-src 'self'
  'unsafe-inline'` and `img-src 'self' data: blob: https:` (`srv/security/headers.go`).
  `ratelimit.Policy` is `{Name, Limit, Window, Message, NoRetryAfter}`: per address only
  (`srv/ratelimit/ratelimit.go:27`). `config.linkField` (`config.go:1789`) is the existing
  rule for a link: an absolute http(s) URL or a same-origin path, nothing else.
  `contracts.CustomerDirectory` returns `CustomerEntry` (`srv/contracts/directory.go:221`);
  `contracts.DueOrder` (`srv/contracts/billable_orders.go:162`) is what Invoices bills.
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
| Generated values | The 10-shade scale (B3), the filled-text colour and the tokens (B2, B36), computed from the inputs, in both schemes, and stored with them. |
| Logos | `mark_light` (for light backgrounds), `mark_dark` (for dark backgrounds), `icon` (square: favicon, collapsed navbar) and `wordmark` (optional, wide: sign-in page and documents). B6. |
| Support | `support_email`, `support_phone`, `support_url`, each optional (today's `APP_SUPPORT_*`), validated by B37. |

`name`, `title`, the colour inputs, the logo slots and the support contacts are **all part
of the versioned content** (B5): editing any of them writes the draft, and none is a
column that bypasses draft and publish. Free CSS is refused: it cannot be checked for
contrast, it breaks on a Mantine upgrade and it is an injection surface. An accent
colour and tinted neutrals are left out on purpose: no component consumes an accent
today, and the dark neutrals were tuned by eye in D-A. Both are future inputs
(Non-goals).

**B2 — A brand customises the existing tokens, and only these.** For each scheme a
brand yields: the ten `vantigo` shades, the filled-text colour (B3), `--vantigo-link`
(light: shade 8, dark: shade 3), `--vantigo-subtle-bg` (light: shade 0; dark: Mantine's
`vantigo-light`, as in D-A), the derived variables Mantine computes from the shades
(B36), and the radius. Body, surface, text, dimmed text and border stay Vantigo's, in
both schemes. `primaryShade` stays `{ light: 6, dark: 6 }`. A brand therefore renders in
light and in dark by construction; there is no brand without a dark rendering.

**D-A leaves two literals that B-A must retire.** In `shell/theme.css`, `--vantigo-link`
is `#3730a3` / `#a5b4fc` and `--vantigo-subtle-bg` is `#eef2ff`: hex values that equal
`vantigo-8`, `vantigo-3` and `vantigo-0` only for the Vantigo scale, so a brand would
leave links and tints in indigo. B-A redefines them as
`var(--mantine-color-vantigo-8)` (light), `var(--mantine-color-vantigo-3)` (dark) and
`var(--mantine-color-vantigo-0)` (light), keeps `theme.test.ts`'s tie to `theme.ts`, and
adds a test that mounts a **non-default brand** and asserts the computed link and tint
change with it.

**B3 — The scale is generated in OKLCH, from one hex, by a fixed algorithm.** The
algorithm is a rule of the design because it must be testable and identical on the
server (Go, the truth, which validates and stores) and in the editor's live preview
(TypeScript, a port):

1. **Colour space.** sRGB to linear (the IEC 61966-2-1 transfer function) to OKLab by
   Björn Ottosson's two published matrices (linear sRGB to LMS, cube root, LMS to OKLab,
   and back), then OKLCH `(L, C, h)`. The matrices are copied into one constants file per
   language, tested against each other through the golden file.
2. **Shade 6 is the input, exactly**: its `#rrggbb` is copied, never round-tripped.
   `(L6, C6, h)` are only used to derive the others.
3. Accept the input only if `0.35 ≤ L6 ≤ 0.85` (otherwise `422 brand_primary_range`).
   This keeps the ends of the scale distinct from shade 6; it is **not** a contrast rule.
   Contrast is B4: white text on a colour passes AA only up to about `L ≤ 0.57`, and
   black text from about `L ≥ 0.56`, so the filled-text colour is chosen, not assumed:
   **`filledText` is `#000000` when the relative luminance of shade 6 is above 0.179,
   else `#ffffff`** (Mantine's `autoContrast` with `luminanceThreshold: 0.179` set
   explicitly in the theme, since Mantine's default is 0.3). Both colours pass AA
   (4.5 : 1) at the threshold, so one of the two always passes, and a light brand
   colour yields dark button text instead of a refusal.
4. Lightness. For `i < 6`: `L_i = L6 + (0.975 − L6) · t_i` with
   `t = [1.00, 0.86, 0.68, 0.50, 0.32, 0.16]`. For `i > 6`:
   `L_i = L6 − (L6 − 0.28) · u_i` with `u = [0.33, 0.66, 1.00]` for `i = 7, 8, 9`.
   Lightness is strictly decreasing from shade 0 to 9.
5. Chroma. `C_i = C6 · k_i` with
   `k = [0.12, 0.25, 0.45, 0.68, 0.88, 1.00, 1.00, 0.95, 0.85, 0.72]`; hue `h` is
   constant.
6. **Gamut**, reproducibly. If `(L_i, C_i, h)` is outside sRGB (any linear channel
   outside `[0, 1]` by more than 1e-6), run **exactly 24 bisection iterations** on a
   chroma scale in `[0, 1]` and take the **lower bound** (the last in-gamut value). Never
   clip channels.
7. Output `#rrggbb`: each sRGB channel rounded half-up to an integer.
8. **Algorithm version.** The constants above are `algorithm: 2` (see B3.9). The version is stored
   with every published token set (B5) and returned in the payload (B36); changing any
   constant is a new version, and old published sets are recomputed only by an explicit
   admin action, never silently on upgrade.
9. **One filled-text colour, so the hover is clamped (algorithm 2).** Mantine renders one
   text colour for a filled button and for its hover (shade 7), so `filledText` cannot
   differ between the two. When `filledText` is black, shade 7 is darker than shade 6 and
   can fall below 4.5 : 1 against black (a bright red, orange or blue did, and could not
   be published). Rule: if `filledText` is black and `contrast(black, shade 7) < 4.5`,
   replace shade 7 by the colour on the line from the lightness-derived shade 7 toward
   the input's own lightness (`L7 + (L6 - L7) · t`, same chroma and hue, gamut rule 6)
   with the smallest `t` in 24 bisection steps whose contrast with black is at least
   4.5; if none passes, shade 7 is shade 6. A white-text brand keeps the generated
   shade 7, which only gets darker. Algorithm 1 sets remain as published (B3.8).

Tests (A0): shade 6 equals the input byte for byte; lightness strictly decreases; hue
stays within 2° of the input except for near-grey inputs (`C6 < 0.02`); every shade is in
gamut; `filledText` flips at the threshold; and a **golden file**,
`testdata/brand-scale.json` at one fixed repository path both suites read, holds the
scale of ten inputs including Vantigo's indigo `#4f46e5` and one near each end of the
accepted lightness range. Go and TypeScript are each tested against that file, so they
cannot drift.

**B4 — Contrast is validated, in both schemes, and enforced at publish.** WCAG 2.x
relative-luminance contrast, AA (4.5 : 1 for text), for every pair a brand's colours
touch:

| Pair | Light | Dark |
| --- | --- | --- |
| `filledText` on the filled primary | on shade 6 | on shade 6 |
| `filledText` on the filled hover | on shade 7 | on shade 7 |
| Links on the page | shade 8 on `#f7f8fc` | shade 3 on `#15182a` |
| Links on a card | shade 8 on `#ffffff` | shade 3 on `#1c1f32` |
| Links on a tinted cell | shade 8 on shade 0 | shade 3 on `vantigo-light` composited over the card |

(The page, card and text colours are D-A's values, read from the token layer by the
test, not copied into it.) A draft may be saved failing; the editor shows each pair with
its ratio and a pass or fail. **Publish is refused** with `422 brand_contrast` naming the
failing pairs. Because `filledText` is chosen by B3, the failures that remain are the
links (the hover is clamped by B3.9): for each the editor offers the **nearest primary** (same hue and
chroma, lightness moved) that passes, one click to apply, and says in words when the
cause is a very light primary (links and hover need a darker colour). The refusal is also
in the server because the API can publish without the editor. Campaign overlays (B22)
are checked the same way.

**B5 — Storage is identity's.** Brands are workspace configuration, like
`identity.sso_settings`, and a separate module would cost every registry in AGENTS.md.
The tables (migrations numbered **at merge time**, after main's latest: the next free
number after 00205 today, and parallel stacks have collided before):

- `identity.brands`: `id uuid`, `is_default boolean` (a **unique partial index** allows
  one row `WHERE is_default`), `state` (`draft` | `published` | `archived`), `published
  jsonb` (the live version: name, title, inputs, generated tokens and `algorithm`,
  logos, support; shape validated by a Go type; `version int`), `draft jsonb` (the
  pending version, null when none), `revision int` for optimistic concurrency
  (`If-Match`, as other identity resources do), timestamps. The **name used for
  uniqueness** is a generated column over `coalesce(draft, published)->>'name'` or a
  separate `name_key`, kept equal by the writer; the choice is made in the PR.
- `identity.brand_rules (id, brand_id, position, kind, target_ref, …)`: B8.
- `identity.brand_logos (sha256 char(64) PK, content_type, object_key, created_at)`: the
  asset registry; B6.
- `identity.brand_campaigns`: B22.
- `identity.users.preferred_brand_id uuid NULL REFERENCES identity.brands ON DELETE SET
  NULL`: the personal choice, B12. It is a **preference**, like `color_scheme`: it is kept
  when a user is anonymised, and a test in B-B asserts the erase path leaves it alone
  (`identity/export_test.go` holds test hooks, not a column registry, so there is no
  registry to join; B-B checks the erase code for a column list).

`jsonb` holds the version because a published version, a draft and a campaign overlay
are the same shape, and a brand is read whole, never queried by a token. The default
brand always exists: the first mount that finds none creates it (B30), so resolution
never has "no brand".

**B6 — Logo assets reuse the invoices pipeline and are content-addressed.** The
validation (type sniff and `DecodeConfig` agree; PNG or JPEG only, never SVG; 512 KiB;
16–2000 px; re-encoded to strip metadata) is the invoices logo's, **extracted into a
shared package** rather than copied (delivery A0): `judgeLogo`, `readLogo`, the
re-encoding and the bounds move out of `srv/invoices/logo.go` into a small package that
imports no business module; Invoices and Identity both import it, and Invoices' tests
move with it and pass unchanged. Objects are stored in identity's storage scope as
`brands/logo-{sha256}.{png|jpg}`, written before the transaction, never deleted (a
document may snapshot one, B27). A version's slot names a `sha256` that exists in
`identity.brand_logos`. **`PUT /brands/{id}/logos/{slot}` writes the draft**, never the
published version, and is read back by `GET /brands/{id}/logos/{slot}?draft=true`
(permission `identity:brands`, streamed from the store; B15). The `icon` must be square
within 5 %; the marks and the wordmark may be any shape. Without object storage
configured, brands work without logos and the upload controls say why.

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
list, inert, and the editor says why. Resolution calls it for **one user** per request
and for the candidates; B18 keeps a publish from turning every open session into a
Staff query at once.

**B10 — Resolution is one function.** `Resolve(user) → {brand, source}` with `source`
one of `choice`, `rule:<id>`, `default`. It is used by `GET /brand`, by the "who sees
what" lookup and by documents' defaults where a user is needed. Its candidate set (B12)
is computed in the same pass.

**B11 — Who sees what.** `GET /brands/resolution?userId=` (B33) answers, for a person,
the resolved brand, the source (the matched rule's position and kind, or "personal
choice" or "default"), and the candidate brands. It reveals **no employment detail**: the
person's legal entities and groups are named only as far as the matched rule's own target
(the rule that matched is already visible in the list). The fuller explanation — all the
groups and legal entities the person has — is added only when the caller also holds the
Staff permission that reads employment data (the same one Staff's person page uses; the
exact key is read from Staff's catalogue in B-B). It needs `identity:brands`.

**B12 — A person in several brands may switch.** The **candidates** of a person are the
brands of *every* rule that matches them; the **default brand is a candidate only when no
rule matched**. If there is more than one candidate, the account menu shows a *Brand*
switcher listing them. The choice is stored per user (`preferred_brand_id`) and **beats
all rules** while it is a candidate; when rules change so that it no longer is,
resolution ignores it (`GET /brand` says so with `choiceIgnored`, so the switcher can
show the reset). `PUT /account/brand` with a brand that is not a candidate answers
`409 brand_not_candidate`. Choosing "Automatic" clears it. A person with one candidate
sees no switcher. The choice is per user, not per device.

### Resolution and delivery to the browser

**B13 — `GET /api/v1/identity/brand` (session) returns the resolved brand.** The body is
the **payload of B36**: `brandId`, `version` (the published version plus the campaign
id when one applies — the cache key), `name`, `title`, `tokens` for light and dark,
`logos` as URLs (B15), `support`, `source` (B7), `candidates` (id and name),
`validUntil` (the next time a campaign starts or ends, B24) and `choiceIgnored`. It is
`x-vantigo-access: session`, `x-vantigo-api: never` (a browser concern). A holder of
`identity:brands` may add `?preview={brandId}` to get that brand's **draft**, for B20.

**B14 — The runtime theme is a function of the brand.** `vantigoTheme` becomes
`createVantigoTheme(brand)`, which fills `colors.vantigo` from the brand's shades and
sets `defaultRadius`, `autoContrast: true` and `luminanceThreshold: 0.179` (B3); the provider in `host/main.tsx` receives it from a `BrandProvider`
that holds the resolved brand and re-renders on change. The theme with no brand is the
Vantigo default brand exactly, so nothing changes for an installation that never edits
its brand. A `useBrand()` hook gives components the title, logos and support. The shell
chooses `mark_dark` or `mark_light` by the colour scheme, and the collapsed navbar uses
`icon`. A brand without a dark mark uses its light mark on a surface chip in dark.

**B15 — Logo files load without a session.** `GET
/api/v1/identity/brand-assets/{sha256}.{png|jpg}` is `x-vantigo-access: anonymous`,
served `immutable` with a long `max-age`, `nosniff`, rate-limited per address, and
answers **only for an asset a published brand or active campaign names** (otherwise 404,
so the route is not a way to read an unpublished draft's logo). A draft's logo is read
through `GET /brands/{id}/logos/{slot}?draft=true`, behind `identity:brands`, so the
editor can show an uploaded-but-unpublished logo. The signed, expiring
`communications/public_media.go` is a model for the handler's shape, not for its
addressing. The CSP's `img-src 'self'` already allows the route. If the public host
filters by path, the route is added there in B-E only when the pay page shows a brand
logo.

**B16 — The first paint is right because the server sends the brand as a
render-blocking stylesheet.** `index.html` gets `<link rel="stylesheet"
href="/api/v1/identity/brand.css">`, rewritten under the base path like the other assets
(`render` in `srv/web/index.go` already rewrites `href="/`). The route
`GET /api/v1/identity/brand.css`:

- returns `:root{…}` and `:root[data-mantine-color-scheme="dark"]{…}` blocks with the
  ten shades, `filledText`, the derived variables (B36), `--vantigo-link` and
  `--vantigo-subtle-bg` if the token layer still sets them, and the radius;
- **without a session** returns the default brand's variables; **with a valid session
  cookie** returns the session user's resolved brand (B10), so the first paint is the
  person's own brand on a new device too;
- is same-origin, so `style-src 'self'` allows it (no CSP change, no new hash);
- is `Cache-Control: private, no-store` with an `ETag` of the payload version and
  `Vary: Cookie` (a shared cache must never serve one person's brand to another, and a
  CDN may ignore `Vary`, so a session's answer is not stored at the edge at all); an
  anonymous answer for the default brand is `public, max-age=60`;
- is `x-vantigo-access: anonymous` whose handler **reads the session cookie itself if one
  is present** and never fails for want of one. If the router has no such mode, B-A adds
  the smallest one (`optional-session`) rather than making the route a 401.

`localStorage["vantigo-brand"]` (`{v: 1, userId, …}`, B36) is **only a fallback** for
when the stylesheet cannot load (offline, a failed request): the pre-paint snippet in
`runtimeConfigScript` applies it through `style.setProperty` if the stylesheet's marker
variable `--vantigo-brand-loaded` is missing. The mirror is **cleared on sign-out and
ignored when `userId` differs** from the session's, so a shared computer never paints the
last person's brand. The snippet stays one script, so the CSP hash stays correct by
construction and `TestNewIndex_ScriptHashCoversExactlyTheInjectedScript` keeps passing.
Once `GET /brand` has loaded, the Mantine theme owns the variables and the stylesheet's
values are equal by construction (B36). The Vite dev server proxies the route; dev has
no injected script. A browser check in B-A confirms the stylesheet reaches Mantine's
derived variables (`--mantine-primary-color-filled` and friends).

**B17 — Title, logo and favicon never regress to a "Vantigo" flash.** The index cannot
see the database, but it must not lose what `APP_TITLE` and `APP_LOGO_URL` give today.
So: until the import (B30) has run, `window.__VANTIGO_APP__` **keeps `title`, `logoUrl`
and `support`** exactly as now and the env stays honoured (the rollback story of B30);
after it, they are dropped from the injected script and the static `<title>` is
`Vantigo`. In both cases the brand stylesheet (B16) and a small anonymous
`GET /brand/public` response give the real values at once: the client sets
`document.title` and the favicon from the payload as soon as it arrives (one request,
cached for 60 s), and the shell reads title, logos and support from `useBrand()`.

**Which pages read `GET /brand/public` (default brand, or the domain's brand, B25):**
setup, sign-in, forgot-password, reset-password, accept-invitation and the
session-expired screen. **Signed-in pages** read `GET /brand`. The three **public pages
that never call `/account`** keep that property: `/pay`, `/rsvp` and `/station` have no
session to resolve a brand from, so `useBrand()` returns the **default brand** there (it
reads the brand stylesheet and `GET /brand/public` without a domain, never `/brand`),
until B-E gives `/pay` the invoice's snapshotted brand.

**B18 — Open sessions repaint live.** Identity declares two topic kinds: `identity.brand`
(no key: workspace-wide changes) and `identity.brand:{userId}` (a person's own switch);
both are authorised for any signed-in person, the second only for their own id, and the
events carry no data. Publish, make default, archive, restore, delete, a rule-list change
and a campaign change `live.Notify` `identity.brand` **in the writing transaction**; a
personal switch notifies `identity.brand:{userId}`. A session that hears either refetches
`GET /brand` (which is checked, so a person never receives a brand they do not resolve
to). **No stampede:** the client waits a random 0–5 s before refetching a workspace-wide
event, and the server resolves a user's brand behind a short per-user cache (30 s,
invalidated by the events above in the same process) so a publish on a thousand open
sessions does not become a thousand Staff queries at the same instant. Time-based changes
(a campaign starting) need no notification: `validUntil` (B13) tells the client when to
refetch, bounded by B24.

**B19 — Favicon and tab title.** The client sets `<link rel="icon">` to the brand's
`icon` (the Vantigo icon, with D-A's dark variant, when none) and `document.title` to
`<page> — <brand.title>`, the form the shell already composes with the instance title.
One favicon per brand: the editor previews it on a light and a dark tab strip so the admin
can see it fail.

### The editor and the screen

**B20 — Draft, "preview as me", publish.** Editing a published brand (name and title
included, B1) writes `draft`;
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
in the workspace's business time zone), `yearly boolean`, `overlay jsonb` (any of: a
different `primary`, different logo slots, a different `title`), `affects_email boolean`
(default false), `enabled boolean`. The overlay is the same shape as the brand's version
with every field optional; a field it omits is the brand's. Overlay tokens are
**recomputed on the server** through B3 and B4 (B38), so a campaign is as readable as a
brand. Campaigns do not change the radius or the support contacts. **A campaign goes
live on save when `enabled`**: it has no draft and no publish step, so the editor says
so and offers "preview as me" with the overlay before saving.

**The pure function `active(campaign, date)`** (the one rule; the resolver, the overlap
check and the tests all call it; `date` is a calendar date in the business time zone):

- **Not yearly:** `starts_on ≤ date ≤ ends_on`.
- **Yearly:** use month and day only; `starts_on`'s year is the **first active year**
  (the campaign is never active before it). Let `s` and `e` be the (month, day) of
  `starts_on` and `ends_on`. If `s ≤ e` the campaign is active on the dates whose
  (month, day) is in `[s, e]`; if `e < s` the range **wraps the year end**, active in
  `[s, 12‑31]` and `[01‑01, e]` (1 Dec – 6 Jan). **29 February is treated as 28 February
  in a non-leap year** (a range starting or ending on 29 Feb starts or ends on 28 Feb
  then). A yearly range may not span more than **365 days** (`422 campaign_too_long`).
- For the wrapping case, the year of the *end* part is the start year plus one; the
  "first active year" rule applies to the start part.

**B23 — At most one campaign is active per brand at a time.** On save, the server
evaluates `active` for the new campaign and for each existing enabled campaign of the
brand **over every date of a two-year window** starting at the earliest `starts_on`
involved (or, if all are dated and none yearly, the union of their ranges); any shared
date refuses with `409 campaign_overlap`. The window is two years so that a yearly range
wrapping New Year and a leap day are both exercised. The check runs under a row lock on
the brand. The resolved brand (B13) is the brand with its active campaign's overlay folded
in, and the response's `version` reflects it.

**B24 — What a campaign changes, and what never.** It changes the **workspace look**
(the shell, the sign-in page for the brand's domains, the favicon) and, **only when
`affects_email` is ticked**, the brand mark in e-mails the system sends in that brand
(when system e-mail carries one: it is plain text today, so the flag is stored and has no
effect until HTML system mail exists — said in the admin guide). It **never** changes an
issued or sent document: an invoice, a credit note, a reminder letter, a sent quote or a
confirmed order carry the plain brand (B27, B28). `validUntil` (B13) is the next boundary
of any campaign of the person's brand. The client refetches at
**`min(validUntil − now, 6 h)`** and also re-evaluates on `visibilitychange` (a tab
that slept past a boundary, or a laptop that suspended, repaints when it wakes); there is
no worker.

### Sign-in

**B25 — Sign-in is branded by e-mail domain, with no leak.** `GET
/api/v1/identity/brand/public?domain=acme.no` is `x-vantigo-access: anonymous`,
`x-vantigo-api: never`:

- **Keyed on the domain only.** The client takes what follows the `@` of the e-mail
  field. The request never carries the local part, and the server never sees it, so it
  cannot answer differently for an address that has an account. It answers the same for
  every address on the domain. A **valid-looking domain** has at least one dot, labels of
  letters, digits and hyphens, and a final label of 2 or more letters; anything else is
  `400 domain_invalid`, and the client does not call.
- **Only attached domains.** If the domain is attached to a brand by a `domain` rule
  (B8) of a published brand, the answer is that brand's public look (name, title, tokens,
  logo URLs, the campaign overlay in force); otherwise it is the **default brand** with
  `matched: false`. Without `domain` it is the default brand. Archived or draft brands
  are never answered.
- **Rate-limited** per address, like the other anonymous identity endpoints: a
  `ratelimit.Policy` of 30 per minute with `Retry-After` (the router's limiter is per
  address only, so there is no global ceiling); over the limit, the client keeps the brand
  it has.
- **Cacheable**: `Cache-Control: public, max-age=300`, normalised domain in the key.
- **Constant work**: one indexed read; a matched and an unmatched domain differ only in
  content.
- **Observed.** The lookup's rate, matched share and 429s are counted and logged at info
  with the *domain hash*, never the domain (B39).

**B26 — The page fades into the brand as the e-mail is typed.** The sign-in page (and
*forgot password*, *accept invitation*, *reset password*) starts in the default brand
(B16's stylesheet). When the e-mail field has a valid-looking domain (B25) and is
**blurred, or has been still for 400 ms**, the client calls B25 once per distinct domain
and, on `matched: true`, cross-fades the colours, the title and the wordmark over 250 ms
(respecting `prefers-reduced-motion`: no fade, an instant change). Editing the domain back
to an unattached one fades to the default. The fade is cosmetic; it never gates sign-in.
SSO buttons are unaffected. **Privacy**, stated in `A/branding.md`: a brand's name,
colours, logos and title become readable by anyone who types a domain an admin attached
— they are public brands, and an admin must not attach a domain whose brand is
confidential; the lookup reveals that *a domain is attached*, never whether any person has
an account; and the domain being typed appears in the server's **access logs** (as a query
parameter) and in any proxy in front of it, which is the reason the local part is never
sent.

### Brands on documents

**B27 — A document carries a brand and snapshots it when it stops being editable.**
Invoices and Orders gain `brand_id uuid` (no foreign key: another module's table,
checked through the contract of B29) and a snapshot of the brand's `name` and logo
(`brand_name varchar(100)`, `brand_logo_object_key`, `brand_logo_sha256`, with the
`CHECK ((key IS NULL) = (sha IS NULL))` pattern of `ck_invoices_seller_logo`).

- **Invoices** snapshot **at issue**, beside the existing `seller_logo_*`. The logo is the
  brand's `mark_light` (a document is printed on white; a brand without one falls back to
  the seller's logo setting), copied by Invoices into **its own** storage scope under
  `branding/logo-{sha256}` — the pipeline it has today — from the bytes
  `contracts.Brands.LogoBytes` returns (B29). The PDF, reminder and e-mail code,
  `seller_logo_*` immutability and the "never deleted" rule are untouched.
  `invoices.refuse_issued_document_change` compares the whole row, so the snapshot is
  immutable once issued.
- **Quotes and orders** snapshot **when they are sent (a quote) or confirmed (an order)**,
  the equivalent moment, into **Orders' own scope** by the same copy (`branding/logo-{sha256}`).
  Reason for a copy over a live read through `contracts.Brands`: a sent document must
  print identically years later and must survive Identity's brand being archived or its
  object store scope changing; the copy keeps every document module self-contained in its
  own scope and makes immutability a property of the document's own row. E3 verifies how
  a quote is rendered today (the Orders module has no logo code; if Invoices renders it
  through `contracts.SellerProfile`, `SellerProfile` gains the snapshot's
  `BrandLogoStorageKey` instead of Orders holding bytes).
- A **draft** document may change its brand freely; a sent, confirmed or issued one never.
  Changing, archiving or campaigning a brand later never changes any of them. **No
  campaign overlay is ever applied to a document (B24)**, and `contracts.Brands` never
  returns one.

**B28 — Which brand a document gets.** In order: the person's explicit choice on the
document (the switch in the editor of an invoice or quote; a draft may change it freely);
else the **customer's default brand**; else the default of the **customer's group**;
else the **default brand**. A brand archived since is skipped (the next in order applies,
and the editor says so). Where no person chooses:

- a **credit note inherits its invoice's brand** (the snapshot, not a fresh resolution);
- **worker-generated invoices** (subscriptions, projects, energy: any invoice a worker
  drafts) take the customer's default, then the group's, then the workspace default;
- an **order invoiced** starts in the order's brand; a **subscription** has its own
  `brand_id` (set like an order's) and its invoices start in it.

**Contract changes this needs, one per consumer:** `contracts.CustomerEntry`
(`srv/contracts/directory.go`) gains `BrandID *uuid.UUID` and `GroupBrandID *uuid.UUID`,
so any module that already reads a customer gets both without a second call
(`CustomerDirectory.Customer`/`Customers` fill them); `contracts.DueOrder`
(`srv/contracts/billable_orders.go:162`) gains `BrandID *uuid.UUID`, so the invoice a
worker drafts from an order or subscription starts in its brand. Customers adds
`brand_id` to `customers.customers` and `customers.customer_groups`, editable by whoever
can edit the customer, shown on the customer and group pages and offered through a brand
picker fed by `contracts.Brands`.

**B29 — `contracts.Brands`.** A single-provider, read-only, in-process slot served by
Identity (`Module.Brands`), wired into every module's `Deps`; always present while
identity is (identity is mandatory). Methods: `List(ctx)` (id, name, state, default),
`Get(ctx, id)` (name, title, tokens, logo slots), `LogoBytes(ctx, sha256)` (a published
or archived brand's logo, for the copy at issue). It performs no authorization: a
consumer is gated on the permission to edit its own record. It never returns a draft or a
campaign overlay. Consumers test against a **fake** `contracts.Brands`, so no delivery's
tests need Identity's real brands.

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
`identity.sso_settings` (`srv/identity/sso_settings.go`).

1. **Creation is race-safe across replicas.** At mount, `INSERT INTO identity.brands …
   ON CONFLICT (is_default) WHERE is_default DO NOTHING` (the unique partial index of
   B5); the replica whose insert returns a row owns the import, the others read the
   existing row. The default is created from the built-in Vantigo values plus whichever of
   the variables are set (title, support contacts, validated by B37 with the same
   `linkField`/address rules as `config`), and is **published at creation**.
2. **The logo is fetched off the mount path.** A background task started after mount (not
   inside `Mount`, so a slow host cannot block boot), bounded by a 15 s timeout, fetches
   `APP_LOGO_URL` **only over http or https, following no redirect**, with a **2 MiB
   read cap** (then B6's validation). A same-origin path is read from the app's own
   public host, not by a network call to itself. The client's transport is built the way
   `srv/identity/webhooks_client.go:105` builds its own — `netguard.DialContext(resolver,
   dial)`, no proxy, short dial and header timeouts — **because `Deps.HTTPTransport` is
   nil in production and is not a guard.** A private, loopback, link-local or
   cloud-metadata address is refused (`169.254.169.254`, `10.0.0.0/8`, `::1`, …) and
   counts as a fetch failure.
3. **Failure** (unreachable, refused address, not an image, over the cap, no object
   storage) imports everything else, logs the reason at warn, and raises a notice in the
   editor (*APP_LOGO_URL could not be imported: upload the logo*); the logo step is not
   retried automatically, and the admin can re-run it from the notice.
4. **Rollback story.** The variables stay **honoured** (B17: injected into the index
   exactly as today) **until the import has run to completion**, and the importing code
   records that in the default brand's row (`imported_env_at`). A release rolled back
   before that moment loses nothing; one rolled back after it finds the env still in
   place and the brand untouched. The variables are **ignored since**, with a boot log
   line when they are still set; `config.Branding` is deleted from `config` one release
   after the import has shipped, and `deploy/compose/vantigo.env.example`,
   `A/configuration.md` and `A/installation.md` are updated.

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
(`brand`, `brand/public`, `brand.css`, `brand-assets`) and the default on the rest, so integrators
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
| Set / remove a logo (writes the draft) | `PUT`, `DELETE /brands/{id}/logos/{slot}` (multipart `file`) | `permission:identity:brands` |
| Read a draft or published logo | `GET /brands/{id}/logos/{slot}?draft=true` | `permission:identity:brands` |
| The brand stylesheet | `GET /brand.css` | `anonymous` (reads the session cookie if present, B16) |
| Campaigns | `GET`, `POST /brands/{id}/campaigns`; `PUT`, `DELETE /brands/{id}/campaigns/{cid}` | `permission:identity:brands` |
| Rules | `GET`, `PUT /brand-rules` (the whole ordered list, `If-Match`) | `permission:identity:brands` |
| Who sees what | `GET /brands/resolution?userId=` | `permission:identity:brands` |
| Targets for pickers | `GET /brand-rule-targets` (groups, legal entities if Staff is on) | `permission:identity:brands` |

Errors follow identity's problem shape: `brand_contrast`, `brand_primary_range`,
`brand_support_invalid` (B37), `brand_not_candidate` (B12), `brand_in_use` (archiving the
default, deleting a published brand), `campaign_overlap`, `campaign_too_long` (B22),
`rule_duplicate`, `domain_taken`, `domain_invalid` (B25), `logo_unsupported_type`,
`logo_too_large`, `logo_too_wide`, `logo_too_small`, `logo_invalid`, `logo_not_square`,
`stale_revision`.
Anonymous routes carry the router's rate limits from `limits`; the logo upload gets the
invoices upload's body bound (it stays under the router's 1 MiB default).

**B34 — Audit.** Each write goes through identity's `writeAudit`, in the changing
transaction: `brand.created`, `.updated`, `.published`, `.archived`, `.restored`,
`.deleted`, `.draft_discarded`, `.default_changed`, `.logo_set`, `.logo_removed`, `brand_rules.changed`
(before and after, as ordered ids), `brand_campaign.created|updated|deleted`. Details
carry names, versions and kinds, never the logo bytes. A person's own choice of brand is
not audited (it is personal preference, like the colour scheme).

### Payload, inputs and observability

**B36 — The brand payload is versioned and carries the derived variables.** Mantine
computes more than the ten shades: `-filled`, `-filled-hover`, `-light`, `-light-hover`,
`-light-color`, `-outline`, `-outline-hover` for the primary colour, in each scheme. A
brand that shipped only shades would leave hovers and light variants in indigo until the
Mantine theme mounted, and the stylesheet (B16) would paint them wrong. So the payload's
`tokens.light` and `tokens.dark` each carry: `shades[10]`, `filledText`, `link`, `subtleBg`
and the derived set as ready CSS variable values. The derived set is **generated on the
server by a function that mirrors Mantine's `default-variant-colors-resolver` rules**
(Mantine 9: `esm/core/MantineProvider/color-functions/default-variant-colors-resolver`),
and a frontend test mounts a `MantineProvider` with `createVantigoTheme(brand)` and asserts
every one of those variables equals the server's for the golden brands, so a Mantine
upgrade that changes a rule fails the test instead of drifting. The payload and the
localStorage mirror are `{v: 1, algorithm: <current>, …}`; a client that sees a higher `v` than it
knows ignores the mirror and refetches.

**B37 — Inputs are validated; tokens are never accepted.** The API accepts **inputs
only**: `name`, `title`, `primary`, `radius`, the logo slots, the support contacts and a
campaign's overlay inputs. **Tokens are always recomputed server-side**, never taken
from a request, a draft written by a client, the JSON import, or a campaign overlay; a
request that carries `tokens`, `shades` or the derived set is `400`, not ignored. The
JSON import (B21) reads inputs and recomputes. Support fields are validated exactly as
the environment's are (`config.linkField` and the address check in `config.branding`,
`srv/config/config.go:1772-1800`): `support_url` is an absolute http(s) URL or a
same-origin path starting with a single `/`, nothing else (never `javascript:`, never
`//host`, never `/\host`); `support_email` is a plain address that equals its parsed
address; `support_phone` uses only digits, spaces, `+`, `-`, `(`, `)` and `.`, at most 30
characters, so it is safe in a `tel:` link. A violation is `422 brand_support_invalid`
naming the field. The same code is used by the import (B30) and the editor. Title, name
and support are rendered as text by React; none is ever injected as HTML or into the
stylesheet, and the stylesheet (B16) is built only from the generated hex values.

**B38 — Campaign overlays go through the same validation.** An overlay's `primary` is an
input and gets B3 and B4 (and `brand_primary_range`); its logos are B6 assets; its
`title` is B1's; an overlay that fails publish-time rules is refused at save.

**B39 — Observability.** Counters (the project's metrics package) and info-level logs for:
the `brand/public` lookup (total, matched, 429, and the domain's hash), the default-brand
import (started, completed, logo failed and why), brand publishes, and `brand.css`
requests answered from cache versus rendered. A rising 429 rate on the lookup is the
signal of enumeration probing.

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

1. **Locking a rule (B12).** A person may switch among their candidates. Should an Owner
   be able to **lock** a rule, so people it matches cannot switch away — a compliance
   option for a company where a brand is a legal matter, not a taste? Default if
   unanswered: no lock.
2. **The brand on an invoice when the customer's default brand is archived (B28).** The
   design falls back to the next default. Should the editor instead **block issuing**
   until the author picks a brand? Default if unanswered: fall back, with a notice.
3. **Brand name on documents.** Should a document print the **brand name** (e.g. under
   the logo) beside the seller's legal name? Default if unanswered: logo only, and the
   brand name is stored in the snapshot for later use.
