# Dark theme and brands — research

Date: 2026-11-18. Status: research, before a design spec.

Two features, in order:

1. **A global dark theme** in Vantigo and on vantigo.io that feels the same in every app
   as today's light theme does.
2. **Brands**, managed at `/workspace/theme`: a company stores several themes (colours
   and logos), and each person sees the one assigned to their office, department or
   group. The motivating case is one company that trades under several brands and wants
   each office or department to work in its own brand.

The second builds on the first. Every brand needs a light and a dark rendering, so the
dark theme comes first and defines the token set that a brand customises.

---

## 1. Where theming stands today

- **One Mantine theme for everything.**
  - `vantigoTheme` (`packages/frontend-shell/src/theme.ts`) is an indigo scale anchored
    on `#4f46e5`, set as `primaryColor`, with `defaultRadius: "md"`.
  - Every business app is a library rendered inside the host SPA. There is exactly one
    `MantineProvider`, in `apps/host/frontend/src/main.tsx:57`. That is good news: a
    colour scheme set there reaches every app at once.
- **Global CSS.** `packages/frontend-shell/src/theme.css` pins the `body` background
  (`#f7f8fc`) and text colour (`#182033`), overriding Mantine's own body colours.
- **No colour-scheme code anywhere.** There is no `useMantineColorScheme`,
  `ColorSchemeScript`, `prefers-color-scheme` or `light-dark()`.
  - The one exception: the FullCalendar CSS already has a dark block
    (`apps/calendar/frontend/src/styles/fullcalendar.css:92-117`).
  - Mantine's tiptap styles already carry dark rules, so the Knowledge editor and its
    code highlighting adapt.
- **Branding is instance-tier, from the environment.**
  - `APP_TITLE`, `APP_LOGO_URL` and `APP_SUPPORT_*` are parsed into `config.Branding`
    (`apps/server/internal/config/config.go:41`, `:1772`).
  - `apps/server/internal/web/index.go` injects them into `index.html` once at startup
    as `window.__VANTIGO_APP__`. That inline script's SHA-256 is pinned in the CSP
    (`apps/server/internal/security/headers.go:40`).
  - The shell shows `config.logoUrl ?? vantigoLogo`
    (`packages/frontend-shell/src/app-shell-layout.tsx:147`).
  - The sign-in page shows only the title, no logo.
- **The logo** (`packages/frontend-shell/src/assets/logo.png`, also the favicon and the
  docs logo) is a navy-to-mint "V". Its dark navy stroke almost vanishes on a dark
  background, so there is no logo that works on dark today.
- **Single tenant.** One workspace per instance; settings tables are single-row.

---

## 2. Feature 1: the dark theme

### 2.1 Mechanism (recommended)

- **The provider.** `MantineProvider` gets `defaultColorScheme="auto"` and a
  `localStorageColorSchemeManager`. Mantine then sets `data-mantine-color-scheme` on
  `<html>`, and every Mantine variable switches with it.
- **A personal preference**, light, dark or automatic (automatic follows the OS).
  - It is stored as `identity.users.color_scheme` (nullable means automatic) and
    follows the exact path of `preferred_language` and `time_zone`: the migration,
    `UpdateAccountProfile`, `GET`/`PATCH /api/v1/identity/account`, the profile form at
    `routes/settings/profile.tsx`, and a sync component like
    `account-language-preference.tsx`.
  - A quick toggle in the account menu, as well as on the profile page.
  - The server value is the truth. Local storage is a mirror, so the first paint is
    right before `/account` has loaded.
- **No flash of light on load.** Append a small pre-paint snippet to the string
  `runtimeConfigScript()` returns (`index.go:82`). The snippet reads local storage or
  `matchMedia` and sets the attribute on `<html>`. The CSP hash then still covers
  exactly one script and stays correct by construction
  (`TestNewIndex_ScriptHashCoversExactlyTheInjectedScript` keeps it honest).
  - Add `<meta name="color-scheme" content="light dark">` to
    `apps/host/frontend/index.html`.
  - The Vite dev server has no injected script, so dev needs its own fallback.
- **Public pages** have no account to read the preference from: `/sign-in`, `/setup`,
  `/forgot-password`, `/reset-password`, `/accept-invitation`, `/pay`, `/rsvp` and
  `/station`. They follow the local mirror, else the OS. `/pay`, `/rsvp` and `/station`
  must keep not calling `/account`.

### 2.2 Tokens: the part that makes it "feel the same"

Today's look is Mantine defaults plus an indigo scale and two body colours. To feel the
same in dark, define a small **semantic token layer** in `frontend-shell` and make both
schemes fill it. That same layer is what a brand later customises (§3).

- Body background, surface, text, dimmed text, border and link colour, each with a
  light and a dark value. `theme.css` stops pinning `body` and uses
  `--mantine-color-body` and `--mantine-color-text`, driven by the tokens.
- **The primary scale in dark.** Mantine uses shade 8 or 9 for the dark filled variant
  by default (`primaryShade: { light: 6, dark: 8 }`). Indigo at shade 8 is muddy on
  dark grey, so tune `primaryShade` and the `dark` grey scale. A slightly blue-tinted
  dark grey is closer to today's `#f7f8fc` feel than Mantine's neutral grey. This is
  the main design decision and wants an eye, not just code.
- Links in `vantigo-8` (in `host/styles.css:15` and `communications/styles.css:14`)
  become a token, because shade 8 is too dark on dark.

### 2.3 The sweep: what breaks in dark today

The codebase is in good shape. Most colour already comes from Mantine props and
variables that adapt: 736 `variant="light"` and 197 `color="gray"` uses are fine. The
survey found **about 85 real problem sites**, and almost half of them are in three
places:

| Area | Sites | What |
| --- | --- | --- |
| host | ~24 | The error boundary's inline hex (it renders outside the provider); `bg="gray.0"` page backgrounds on sign-in, setup, reset, forgot and accept-invitation; the error illustrations' SVG fills; one `vantigo-0` background in users |
| projects | ~17 | The hand-rolled Gantt `-task-timeline-view.tsx`: gray-0..3 headers and borders, the `blue-0` today column, `rgba(34,139,230,…)` |
| communications | ~11 | The yellow highlight bubbles `#fff8df`/`#f1d888`, `vantigo-0` and `gray-1` backgrounds |
| five apps | 5 | The same hand-written drop zone (dashed `gray-4`, `gray-0` hover) in customers, energy, expenses, products and staff. Replace it with **one shared component** in `frontend-shell/ui` |
| the rest | ~28 | Single `gray.0` cards and cells; shade-8/9 text (`red.8`, `teal.9`, `orange.8`, `yellow.8`) that lacks contrast on dark; two SVG strokes in agents; `pay.tsx` and `rsvp.tsx` page backgrounds |

Special cases:

- **E-mail HTML preview** (`communications/api/conversations.ts:398`, in a sandboxed
  iframe). Keep it on a white "paper" card in dark mode. E-mail HTML assumes white,
  and rewriting other people's mail colours is a losing game.
- **Charts** use Mantine colour names, so the grid and tooltips adapt. Shade-6 series
  might want a lighter shade in dark; check by eye.
- **Knowledge co-editing carets** (12 fixed colours with white labels) read on dark,
  but the 1 px caret is dim. Possibly 2 px in dark.
- **The API reference** (`apps/apiref`, Scalar) is its own bundle. Set Scalar's
  `darkMode` from the same local mirror, and fix two hex values in its `style.css`.
- **Docs** (Starlight) already has a light/dark/auto switch. It needs only a dark logo
  (Starlight takes `logo: { light, dark }`). The docs choice is not shared with the
  app; acceptable.
- **Logo and favicon.** A dark-background variant of the Vantigo mark, plus a favicon
  that reads on dark tabs.

Tests: nothing asserts a light colour. Every app's test setup mocks `matchMedia` as
`matches: false`, so tests stay light. New toggle tests override that. There are no
visual-regression suites, so the check is a **browser walk through every app in both
schemes**. That is the real cost of this feature, more than the code.

### 2.4 The landing site (vantigo-landing)

The local checkout is on the stale branch `fix/smoke-waitlist-areas`. Everything below
was read from `origin/main`.

- **Stack.** Astro, plain CSS, tokens in `web/src/styles/tokens.css` that mirror the
  app's palette.
  - Only five semantic tokens exist. Components use the raw scales directly: about 330
    raw references (`--gray-7`, `--indigo-0`, `--mint-0`, …) against about 130 semantic
    ones.
  - About 82 hard-coded colours.
  - The same work as the app's token layer is needed: either a semantic layer, or a
    dark remap of the raw scales. The remap is cheaper, but light tints used as
    backgrounds (`--indigo-0`, `--mint-0`) map badly, so a semantic layer is better.
- **Product scenes.** `web/src/styles/scenes.css` (1457 lines, 104 raw references)
  imitates the app's UI and needs a full dark variant. Copy the app's dark palette
  once it is chosen, so the scenes look like the product. The per-app data files name
  only tones, so they do not change.
- **Pieces that are already dark** need rethinking on a dark page: the navy Security
  band and the AI feature card.
- **Header and meta.**
  - The sticky header is `rgb(255 255 255 / 86%)`.
  - `<meta name="color-scheme" content="light">` and `theme-color` are pinned in
    `BaseLayout.astro:87-88`; both get `media` variants.
- **Logo.** The same dark-navy logo problem; it appears in the header, footer,
  integrations hub and OneSystem.
- **Constraints.**
  - The CSP is hash-based (`server/internal/web/csp.go`), so a pre-paint inline script
    must go through that hasher.
  - `check-budget.ts` forbids `style=""` and caps CSS at 30 KB gzipped. The dark
    palette and scene overrides eat into that budget.
  - Lighthouse requires accessibility ≥ 0.95 but runs only light. Add an emulated dark
    run, or dark contrast is never measured.
- **Toggle or OS only?** A marketing site usually just follows the OS. That is
  simplest and needs no script at all (`@media (prefers-color-scheme: dark)`).
  Recommended; add a toggle later if wanted.

### 2.5 Documentation pages

- `user/getting-started.md` or wherever the profile is described, in en and nb: the
  appearance preference and the toggle.
- Identity has no reference page yet; the new account field goes in `admin/authentication.md`
  (the page AGENTS.md names for identity), or a new `reference/identity.md`.
- `contributing/`: the token layer and "never hard-code a colour". A small lint (a grep
  in `frontend:check` for `#hex` and `gray.0` in `.tsx`) keeps the sweep from rotting.

The landing site can only claim dark mode once the user guide documents it.

---

## 3. Feature 2: brands

### 3.1 What a brand is

The user's framing is "themes". The case they describe, several brands under one
company, is better named a **brand**: a theme is only part of it. A brand would hold:

- **A name**, and the **title** shown in the tab and the shell (today `APP_TITLE`).
- **Colours, as a constrained token set, not free CSS.**
  - A primary colour: one hex from which the 10-shade scale is generated, or an
    explicit scale.
  - Optionally an accent colour, the body and surface tints, and the radius. These are
    the same semantic tokens as §2.2.
  - Both schemes are derived from the same inputs, so every brand works in light and
    dark. Show a contrast check (WCAG AA for text on primary and links on body) in the
    editor, and refuse or warn on failure.
- **Logos.**
  - A mark for light backgrounds and one for dark backgrounds.
  - A square icon for the favicon and collapsed navbar.
  - Optionally a wide wordmark for the sign-in page and documents.
- **Possibly**, support contacts (today `APP_SUPPORT_*`) and later a sender identity for
  documents (§3.6).

Free-form CSS is not recommended. It cannot be checked for contrast, breaks on every
Mantine upgrade, and is a CSS-injection surface.

### 3.2 What a brand can be assigned to

The codebase has two families of org structure, and neither has a precedence concept:

| Target | Where | A person can be in several? |
| --- | --- | --- |
| Access group (identity) | `identity.access_groups`, local or SCIM-synced from the IdP | yes |
| Department | `staff.departments` (a tree), via `employment_terms.department_id` | only with several employments |
| Location / office | `staff.locations`, via `employment_terms.location_id` | same |
| Business unit | `staff.business_units`, via `employments.business_unit_id` | yes, several employments |
| Legal entity | `staff.legal_entities`, via business unit | yes |
| Staff group | `staff.groups` (listed, or rule-derived over department, location, …) | yes |

- A user reaches Staff through `staff.identity_links` (provider `identity`).
- Staff groups are pushed out to Entra and Google, never pulled into identity.
- Employment terms are dated (`valid_from`), so resolution uses the terms in force
  today.

**Recommendation: ordered assignment rules.** A list of rules, each of the form "people
in <target> see <brand>", in an order the admin sets by dragging. The first match wins,
and a default brand applies when nothing matches. This is explicit, explainable ("you
see Brand B because of rule 3: Office Bergen") and handles people in several units.

- Targets in the first delivery: access group, department (including sub-departments),
  location, business unit and legal entity.
- Staff groups can come later; they can already be derived from department and
  location.
- Staff is optional. Without the Staff module, only access groups are offered.

### 3.3 Resolution and delivery to the browser

- The index is templated once at startup with a pinned CSP hash, so a per-user brand
  cannot ride in `window.__VANTIGO_APP__`. Instead:
  - `GET /api/v1/identity/brand` (session) returns the resolved brand's tokens and
    logo URLs. The session response could carry the brand id so the client knows when
    it has changed.
  - The pre-paint snippet (§2.1) also applies the **last-seen brand's tokens** from
    local storage. The first paint is then right for returning users; a first visit
    briefly shows the default brand.
  - Tokens become CSS variables plus a generated Mantine `colors.brand` tuple. That
    means `vantigoTheme` becomes a function of the brand (`createTheme` at runtime),
    and `primaryColor` is the brand's scale.
- **Logo files must load without a session** (sign-in page, favicon, pay page).
  - Serve them from an anonymous, content-addressed endpoint, e.g.
    `/api/v1/identity/brand-assets/{sha}.png`, `immutable`, rate-limited. The model is
    `communications/public_media.go`.
  - Add the endpoint to the public-host allowlist only if the pay page shows brand
    logos.
- **Live change.** A new `identity.brand` live topic lets open sessions repaint when an
  admin publishes a change or reassigns a rule. Identity has no live topics today.

### 3.4 Storage

- Tables in identity, from migration **00203** (check main's latest right before
  merging; several stacks are in flight):
  - `identity.brands`: id, name, title, tokens as `jsonb` validated by a Go type, logo
    object keys and hashes, `is_default`, timestamps.
  - `identity.brand_rules`: id, brand id, target kind and target id, position.
  - Brands are workspace configuration, so identity owning them is consistent with
    workspace settings. A separate `branding` module is possible but costs every module
    registry (AGENTS.md), so it is not recommended.
- **Logos.** Reuse the invoices logo pipeline (`apps/server/internal/invoices/logo.go`):
  - a 512 KiB cap;
  - `DetectContentType` and `DecodeConfig` must agree;
  - re-encode to strip metadata;
  - content-addressed, never deleted.

  Add SVG? No. SVG is a script vector and needs sanitising, so PNG and JPEG only, as
  invoices do (WebP is optional). Object storage must be configured; brands without
  logos still work without it.
- **The environment branding.** `APP_TITLE`/`APP_LOGO_URL` would live in two tiers. Per
  the tiers rule, import them once into the default brand (as `OIDC_*` was) and retire
  them, with a note in `admin/configuration.md`.

### 3.5 The `/workspace/theme` screen

- **List.** The brands, with the default marked.
- **Editor.**
  - Name and title, colour pickers and the logos.
  - A **live preview** in both schemes: a mini shell, buttons, a table, a badge.
  - The contrast check.
  - **Save as draft and publish**, so an admin can try a brand on themselves first
    ("preview as me") before everyone sees it.
- **Assignment.** The ordered rules, and a **"who sees what" lookup**: pick a person and
  see their brand and the rule that matched.
- **Permission.** A new delegable `identity:brands` (the grammar allows it), plus Owner.
  Register it in the admin permission labels.
- **Audit.** Changes to brands and rules go to whatever audit trail identity keeps.

### 3.6 Things probably missed

These are worth deciding before the spec:

1. **Before sign-in, nobody has a brand.** The sign-in page, invitations and password
   reset show the default brand. If brands have their own domains
   (`app.brand-a.no`, `app.brand-b.no`), a **hostname → brand** mapping fixes that. It
   is also what makes multi-brand credible to a buyer. It touches the base path, the
   CSP, cookies and SSO redirect URIs, so it is its own delivery.
2. **Customer-facing output.** Invoices, reminders, quotes and their mails use one
   global seller (`invoices.settings`, one row) with one logo. A company with several
   brands almost certainly wants **Brand B's invoices to carry Brand B's logo** and
   probably Brand B's sender name and bank details. That is multi-seller support in
   Invoices and Orders (`contracts.SellerProfile`, the logo snapshot on issued invoices
   already exists). It is the biggest piece and should be its own phase, keyed on a
   brand chosen per customer, per document or per legal entity.
3. **System e-mails** (invitations, links) are plain text today, so there is no logo to
   brand. HTML system mail would be a separate decision.
4. **Favicon and tab title** per brand. These are cheap once the logo assets exist.
5. **People in no unit, and integration (API key) users.** They get the default brand.
   Rule-derived memberships change as employment terms change, so resolution is
   computed, not stored.
6. **Can a person switch brand?** For example, someone who works for two brands. Option:
   let people pick among the brands their rules match. Simple to add if wanted.
7. **The Assistant** names the product in its prompt. Should it say the brand title?
8. **"Powered by Vantigo".** Should a fully rebranded shell keep any Vantigo mark? This
   is a commercial question.
9. **Export and import of a brand as JSON.** Cheap, and useful between test and
   production instances.
10. **Docs and the API reference** stay Vantigo-branded. Say so in the admin page.

### 3.7 Documentation and the landing site

- New `admin/branding.md` (en and nb): brands, logos, rules, the domain mapping.
- `user/workspace-administration.md` (en and nb): the `/workspace/theme` screen.
- `admin/configuration.md` (en and nb): `APP_TITLE`/`APP_LOGO_URL` retired into the
  default brand.
- `admin/authentication.md`, or a new `reference/identity.md`: tables, the resolution
  rule, the endpoints and the permission.
- The landing site: a platform-level claim (a pillar or card in both catalogs, not an
  app page; `workspace-administration` is on `apps:check`'s `notApps` list), written
  from the guide once it exists.

---

## 4. Suggested deliveries and rough size

| # | Delivery | Size |
| --- | --- | --- |
| **D-A** | Dark foundation: the token layer, `defaultColorScheme`, the account preference (migration, API, profile, menu toggle), the pre-paint snippet, the dark logo and favicon, the shared drop zone | M |
| **D-B** | The sweep: the ~85 sites, host and public pages, the projects Gantt, communications, the e-mail paper, Scalar, the docs logo, plus a colour lint in `frontend:check`; a browser walk of every app in both schemes | M (mostly checking) |
| **D-L** | Landing dark: the semantic layer, the scenes, the header and meta, the logo, a dark Lighthouse run | M |
| **B-0** | Brands design spec (answers §3.6) | S |
| **B-A** | One editable brand: tables, the logo pipeline, public assets, a runtime theme from tokens, the `/workspace/theme` editor with preview, the env import | L |
| **B-B** | Several brands and assignment rules: the resolution over identity and Staff, the "who sees what" lookup, the live topic, local caching | M |
| **B-C** | Hostname → brand for pre-sign-in pages and per-brand domains | M, with infra |
| **B-D** | Brand on customer-facing documents (multi-seller Invoices and Orders) | L, its own phase |
| **B-L** | Landing claim for brands | S |

D-A and D-L can run in parallel. B-A needs D-A's token layer. B-C and B-D are
independent of each other once B-B lands.

## 5. Questions for the owner

1. Should the landing site follow the OS only, or have a toggle?
2. Should a brand also set documents (invoices, quotes) — is B-D in scope at all, and
   is a brand chosen per legal entity, per customer or per document?
3. Do brands get their own domains (B-C)?
4. Which targets matter first: offices (locations), departments, business units, or
   IdP groups?
5. May a person in two brands choose between them?
6. Should any Vantigo mark remain in a rebranded shell?

---

## 6. Owner decisions (2026-11-18)

1. **Landing site:** follows the computer's setting first, plus a toggle.
2. **Documents carry a brand.** Each customer or customer group (`customers` groups,
   migration 00027) can have a default brand; the person writing an invoice or quote
   can switch brand for that one document. **Campaigns** — time-bound brand variants
   (Pride, Christmas, …) — are wanted too.
3. **No per-brand domains.** Everyone uses the same domain. Instead, the sign-in page
   starts in the default brand and, once an e-mail address is typed, **fades into the
   brand that matches its domain** (`name@brandname.com`).
4. **Assignment targets that matter:** groups and legal entities, plus the **e-mail
   domain** (or username) as in point 3.
5. **A person in several brands may switch** between them.
6. **No "Powered by Vantigo" mark** — leave it out.

### Notes for the brands spec

- **E-mail domain → brand before sign-in** must not leak who has an account: resolve on
  the *domain* only, answer the same way for any address, and only for domains an admin
  has attached to a brand. Brand names, colours and logos become public to anyone who
  types a matching domain; that is acceptable (they are public brands) and should be
  said in the admin guide. Rate-limit the lookup like other anonymous endpoints. It
  fits naturally with SSO home-realm discovery, which also keys on the domain.
- **Campaigns** are best modelled as a dated overlay on a brand (from–to, optionally
  every year), changing tokens and/or logos, not as separate brands. Recommended: a
  campaign changes the workspace look and, only if the admin ticks it, e-mails; never
  issued invoices or other legal documents, which should stay in the plain brand.
- **Documents:** the brand on an issued invoice is snapshotted like the logo is today
  (`seller_logo_object_key`), so changing a brand later never changes an issued
  document. Whether a brand also carries a different seller (name, organisation number,
  bank account) is the multi-seller question; a brand with only a different logo and
  colours is much smaller and could come first.
- **Order of rules** with the new targets: e-mail domain, legal entity, group — still
  first match wins, with the person's own choice (point 5) above all rules.
