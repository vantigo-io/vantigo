# Integrations under Workspace admin, delivery A — the Entra page in four tabs: implementation plan

> **For agentic workers:** implement this plan task by task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** an Owner opens the avatar menu, chooses **Workspace admin**, and the sidebar has
a second section, **Integrations** (nb **Integrasjoner**), with one item, **Microsoft
Entra ID**, at `/workspace/entra`. That page is today's Entra page regrouped into four
tabs — **Status**, **Connection**, **Accounts**, **Outlook and mail** — each a route. The
Entra tile is gone from the app switcher and `/entra` redirects to the new page. A new
administration page, *Configuration*, explains the two configuration tiers. Nothing in Go
changes and nothing the Entra module does changes.

**Architecture:** frontend and documentation only. `apps.ts` loses `moduleApp("entra", …)`
(`AppKey` no longer includes `entra`) and the `workspace` area gains a second
`NavSection`, `navigation.integrations`, whose one item carries `module: "entra"` and
`requiredPermissions: ["entra:manage"]`. `routes/workspace.tsx` admits an `entra:manage`
holder while the module is on. `@vantigo/entra-ui` exports `EntraStatusTab`,
`EntraConnectionTab`, `EntraAccountsTab` and `EntraOutlookTab`, moved out of
`pages/entra.tsx`; the one settings form that held the account rules, the Outlook
connection switch and the Mail channels switch becomes two forms over the same
`PUT /connection/settings`, which already keeps every field left out. The host mounts the
tabs as child routes of a `routes/workspace/entra.tsx` layout, and `routes/entra*.tsx`
become permanent redirects. Branch `feat/workspace-integrations`, cut from `main`
(`5108bb4b`) with the spec and the briefs on it (`0e553d10`).

**Spec:** [`docs/superpowers/specs/2026-10-31-integrations-and-configuration-tiers-design.md`](../specs/2026-10-31-integrations-and-configuration-tiers-design.md), T1, T2, T3, T15 (delivery A).
**Brief:** [`…/2026-10-31-integrations-briefs/a-workspace-integrations.md`](2026-10-31-integrations-briefs/a-workspace-integrations.md).

## Plan readings

1. **Where the page's cards really are.** `apps/entra/frontend/src/pages/entra.tsx`
   (`EntraPage`, `:49`) renders `Wizard` (`:68`): `StatusCard` and `GroupsCard` while the
   connection is not `unconfigured` (`:84-85`), then `StepCard`s 1 to 6 (`:87-137`). Step 1
   is `RegisterForm` (`:150`), step 2 `CredentialList` + `AddSecretForm`, step 3
   `PermissionsStep` (`:262`) with `TestResult` (`:326`), step 4 `SettingsForm`, steps 5
   and 6 the `signIn` and `provisioning` render props. The shape the brief and the spec
   describe (four tabs of "today's cards") holds for 1–3, 5–6 and the Groups card, which
   move as they are.
2. **The settings form is one form, not three cards.** `components/settings-form.tsx`
   (`SettingsForm`, `:113`) holds the account rules (`:199-294`), the *Outlook connection*
   switch with its redirect URI, status and *Change notifications* (`:296-314`), the *Mail
   channels* switch with its status and commands (`:316-331`) and one *Save the account
   rules* button (`:332`), validating and saving all thirteen fields together
   (`valuesOf` `:66`, `requestOf` `:86`). The brief says "move the cards, do not
   rewrite"; the Outlook tab cannot be a tab of its own without a form of its own, so the
   form is split by what each half owns. That is possible without a contract change:
   `EntraSettingsRequest` says "Every field is optional, and one left out keeps its value"
   (`openapi/entra.yaml:735`) and `mergeSettings` (`srv/entra/connection.go:429`) does so;
   `PutEntraConnectionSettings` audits only the fields that changed. Each half sends only
   its own fields. The Outlook half therefore no longer waits for the UPN domain to be
   valid, which the old form's single validation did by accident, not by rule.
3. **"Calendar sync" is not a switch of its own.** The brief and the spec (T3's table)
   name *Outlook connection*, *Calendar sync* and *Mail channels* as three. In the code
   there are two switches (`outlookConnect`, `mailChannels`); calendar sync runs on each
   person's own Outlook connection (Outlook design M2, M9) and its state is the
   *Change notifications* block under the Outlook switch. The Outlook tab holds both
   switches with that block; the Status tab has **one line per switch** — two lines — and
   the Outlook line says calendar sync follows it.
4. **Status tab and the Groups card.** T3's table puts the Groups card on *Accounts* and
   mentions "the groups kept" on *Status*; `StatusCard` already shows `Groups: n kept`
   (`status-card.tsx`, the fourth `Fact`). The Groups card goes to *Accounts* as the table
   says. `StatusCard` also holds the client secrets list (`CredentialList`); it stays in the
   Status card unchanged (rotation is on *Connection*, step 2, as today).
5. **`stepsDone`** (`lib/steps.ts`) answers four marks — `register`, `secret`,
   `permissions`, `accounts`; steps 5 and 6 are optional and carry no mark. The spec's
   "*n* of 6 steps done" cannot be counted from it. The callout reads *n of 4 required
   steps done — continue in Connection* (steps 1–3) *or Accounts* (step 4), counted from
   `stepsDone`, and is gone once all four are; the cards keep their numbers 1–6, so the
   administration guide's "step 3" still names one thing. Recorded as a departure.
6. **Links between tabs from the package.** The package already hard-codes the paths it
   links to (`appUrl("/staff/settings")`, `CONNECTIONS_PATH` in `lib/return-to.ts`), and
   `frontend-shell` exports `useShellLink` (`link-context.tsx`), the host's client-side
   link, undefined outside the host (plain anchors then, as `invoiced-by-badge.tsx:24`
   does). The Status tab's lines and callout link with it, to `/workspace/entra/<tab>`.
7. **`return_to`.** `safeReturnTo` (`lib/return-to.ts:13`) accepts any same-origin path
   and falls back to `/settings/connections`; no caller in the repo passes `/entra`
   (`grep` below), so nothing in it changes. A `return_to` of `/entra` that was sealed
   before the release would land on the redirect, which preserves it.
8. **Where `/entra` is named.** `grep -rn '"/entra' apps packages` (outside generated and
   `api/v1`): `apps/host/frontend/src/apps.ts:489-494`, `lib/dashboard-helpers.ts:354`
   (the `accountUnconfirmedInEntra` attention item, confirmed on the Status card) and its
   test `routes/dashboard.test.ts:819-820`, `routes/entra.tsx`, `routes/entra/index.tsx`,
   `apps.test.ts:115,140`. Staff's and Communications' catalogs say "the Microsoft Entra
   ID page" in words (`staff/frontend/src/i18n.ts:631`, `:2717`; `catalogs/dashboard.ts:111`)
   and link nowhere; the brief's *Open Entra* text in Staff settings does not exist (the
   Entra page links *to* Staff settings, `entra.tsx:127`). Communications links the docs
   (`channelErrors.tsx:46`, `/docs/<locale>/admin/entra/#mail-channels`), which stays.
9. **What derives from `AppKey`.** `apps.ts:58` `AppKey = "home" | ModuleKey`;
   `appForKey` (`:560`), `areaForKey` (`:567`), `switcherTiles` (`:621`), `allNavSections`
   (`:499`) and `spotlightNavSections` (`:507`) run over `apps`, so removing the app
   removes the tile and the spotlight entry; `components/module-access-guard.tsx:21-36`
   builds its rules from `allNavSections`, so `/workspace/entra` needs its own gate
   (the route's `beforeLoad`); `routes/-app-layout.tsx:20,36` calls
   `appForKey(app)` with a `ModuleKey` and is narrowed to the keys that have an app.
   `StaticDataRouteOption.app` (`:70`) is `AppKey | AreaKey`; the `/workspace/entra`
   subtree keeps `app: "workspace"` from its parent.
10. **The area guard** (`routes/workspace.tsx`) admits an Owner without reading the
    authorization query, then an authorization manager. It will also admit a session whose
    permissions hold `entra:manage` while `enabledModuleKeys()` includes `entra`;
    `getAuthorizationMe` already answers `permissions` (the root reads it,
    `__root.tsx:100`). Order: Owner (no fetch), then the fetch, then
    `canManageAuthorization || (entra on && hasPermissions(permissions, ["entra:manage"]))`.
11. **How a non-Owner gets to the section.** The avatar menu's *Workspace admin* is
    `ownerOnly` and *Roles & access* `capability: "authorization"` (`account-menu.ts:25-26`);
    an Entra manager who is not an Owner has neither. T2 says such a person "sees
    *Integrations* alone", so the menu gets a third entry in its workspace section,
    *Microsoft Entra ID* → `/workspace/entra`, `module: "entra"`,
    `requiredPermissions: ["entra:manage"]` — the shape *Roles & access* already has.
    An Owner sees it too (Owners hold `*`). It also keeps *Microsoft Entra ID* in the
    spotlight, which reads `accountMenuSections` (`apps.ts:507-512`) now that the app's
    sidebar entry is gone. Recorded as an addition to the brief.
12. **Tests that name the old shape.** `apps.test.ts`: the key list (`:30-52`, `:556-572`,
    `:664-672`), the path list (`:115`), the Entra app test (`:134-149`), the area-prefix
    test that allows only `/settings/connections` a module (`:569-571`), the workspace
    visibility test (`:588-598`). `route-guards.test.ts:123-203`: the workspace guards.
    `app-spotlight.test.tsx:7-20` and `parity.test.ts` stay as they are. `pages/entra.test.tsx`
    (1020 lines) renders `EntraPage` throughout; it is split by tab.
13. **Generated route tree.** `routeTree.gen.ts` is committed and written by the router
    plugin on `vite`/`vitest`/`tsc -b` runs of the host; commit its regeneration with
    the routes.
14. **Docs the change touches.** Frontmatter `sources` that name the old route:
    `user/entra.md` (`apps/host/frontend/src/routes/entra`, `…/entra.tsx`), and
    `reference/entra.md`; both gain `routes/workspace/entra`. Coverage:
    `workspace-administration.md` claims `apps/host/frontend` and the identity module.
    `admin/configuration.md` is new (en and nb; the administration section is bilingual) with
    `sources: apps/server/internal/config`. The administration guide's "Open **Microsoft
    Entra ID** from the app switcher" (`en/admin/entra.md:63,74`) and the user guide's
    "Open Microsoft Entra ID from the app switcher" become the new place; "step 4 …
    under Outlook connection / Mail channels" (`en/admin/entra.md:262,278,578-580,661,774`;
    `en/user/entra.md` the same in *Outlook connection* and *Mail channels*) become the
    **Outlook and mail** tab, whose button is *Save* (see task 1).

## Tasks

- [x] **1. `refactor(entra-ui)`: split the settings form.** `SettingsForm` becomes the
  account rules alone (step 4, the leaver and joiner rules, the licences list) sending
  only those ten fields; `OutlookSettingsForm` takes the two switches, the redirect URI,
  `OutlookConnectStatus`, `ChangeNotifications`, `MailChannelsStatus` and
  `MailChannelsCommands` (moved, not rewritten) with its own *Save* button
  (`saveOutlookSettings`) sending `{outlookConnect, mailChannels}` and the *Turn the
  Outlook connection off?* confirmation. `EntraPage` renders both for now. Tests: the
  existing tests that toggle a switch "with the account rules" now assert the narrower
  request body and press the new button.
- [x] **2. `feat(entra-ui)`: the four tab components.** Move `RegisterForm`,
  `PermissionsStep` and `TestResult` out of `pages/entra.tsx` into `components/`;
  add `pages/tab-frame.tsx` (the connection and owner queries, skeleton and error shared
  by the tabs) and `EntraStatusTab`, `EntraConnectionTab`, `EntraAccountsTab`,
  `EntraOutlookTab`; export them from `index.ts` with `entraTabs` (value, path, label
  key); `EntraPage` becomes the deprecated composition. The Status tab: the Status card
  (or the "not connected yet" notice), the *n of 4 required steps done — continue in …* callout
  linking to the tab, one line per switch with its link. New en and nb keys. Tests: split
  `pages/entra.test.tsx` by tab (`status-tab.test.tsx`, `connection-tab.test.tsx`,
  `accounts-tab.test.tsx`, `outlook-tab.test.tsx`), each asserting the cards it took and
  the callout's text; one small test keeps `EntraPage` honest.
- [x] **3. `feat(host,entra-ui)`: Integrations, `/workspace/entra` in four tabs.**
  `apps.ts`: remove `moduleApp("entra", …)`, narrow `AppKey` to the modules that have an
  app, add the Integrations `NavSection` to the workspace area; `account-menu.ts`: the
  *Microsoft Entra ID* entry; catalogs en/nb (`navigation.integrations`,
  `navigation.entra` stays); `routes/workspace.tsx`: the guard; `-app-layout.tsx`:
  narrowed key; routes `workspace/entra.tsx` (gate: module on, else the module-disabled
  not-found; `entra:manage`, else redirect `/`; header, `PageTabs`, `Outlet`),
  `workspace/entra/index.tsx` (Status; `redirect` to `connection` while
  `stepsDone(connection).register` is false), `connection.tsx`, `accounts.tsx` (hands in
  identity's `SsoSettingsCard`), `outlook.tsx`; `routes/entra.tsx` and `routes/entra/index.tsx`
  become permanent redirects; regenerate `routeTree.gen.ts`. Tests: `apps.test.ts` (the
  visibility matrix — Owner, Entra manager who is not an Owner, authorization manager,
  Entra module off — the key and path lists, the menu entry), `route-guards.test.ts`
  (the area guard and the child gate, the redirects), `catalogs/parity.test.ts` unchanged
  and green.
- [ ] **4. `feat(host)`: the dashboard opens the Status tab.** `attentionHref` for
  `accountUnconfirmedInEntra` → `/workspace/entra`; its test.
- [ ] **5. `docs(admin)`: the Configuration page, en and nb.** New `admin/configuration.md`
  (`sources: apps/server/internal/config`): the two tiers table (T1), the rule, today's
  instance settings grouped (process, mail, storage, workers, shared services) with one
  line each, and for every workspace setting the page that edits it; linked from
  `admin/index.md` under *Where to start* in both languages.
- [ ] **6. `docs(entra)`: the four tabs, en and nb.** `user/entra.md` (the new place, the
  tabs and what each holds, every "the page" → the tab, the redirect), `admin/entra.md`
  (the same rewording, the redirect), `reference/entra.md` (the routes, the redirect,
  the exports, the split settings request as the UI sends it); `sources` updated.
- [ ] **7. `docs`: Integrations in Workspace administration, the host's slot, the
  roadmap.** `user/workspace-administration.md` (en, nb): *Integrations* — what it is, who
  sees it, an Entra manager who is not an Owner sees only that section; `contributing/
  module-boundaries.md` (en): the host's *Integrations* section as a slot beside the
  *Connections* page slot (rule 7's paragraph, `:236-241`); `ROADMAP.md`: *Integrations*
  under *Platform*.
- [ ] **8. The gates.** `mise run frontend:check`, `mise run docs:check`,
  `mise run server:check` (to show no Go changed); `git diff --stat 0e553d10.. -- apps/server openapi`
  empty. Fix what they find in the task that caused it.
