# App switcher and per-app shell — design

## 1. Scope

Turn the single flat sidebar into a Google Workspace-style layout: one
frontend, one router, but each business module presented as its own "app"
with its own URL prefix and its own sidebar, switched from a waffle menu in
the top-right of the header. The user menu moves from the bottom of the
sidebar into an avatar menu beside the waffle, and takes the settings and
administration destinations with it.

Goals, in priority order:

1. A user can tell from the URL which app a page belongs to.
2. The sidebar only shows the app the user is in.
3. A module turned off with `MODULES` is visibly "not enabled" in the SPA
   instead of rendering pages that 404 against the API.
4. Adding an app later is one registry entry plus one layout route.

Nothing is in production, so URLs change without redirects or shims.

Explicitly **not** in scope:

- Per-app theming. Every app uses the shared Mantine theme unchanged.
- Any change to backend module names, API prefixes, schemas or package names.
- Extra lazy loading. TanStack's `autoCodeSplitting` already splits every
  route component into its own chunk; nothing more is needed.
- The `password-reset` / `reset-password` route duplication and other
  unrelated route cleanups.

## 2. Decisions

**D1 — URL prefix per app, named after the backend module.** The URL
prefix, the API prefix (`/api/v1/<module>/`), the `MODULES` entry and the
frontend package name all use the same word. This changes CONTRIBUTING's
"flat primary resources, module-qualified secondary ones" convention to
"every app route lives under its app prefix"; CONTRIBUTING is updated
accordingly. Only two destinations actually move: `/contacts` →
`/customers/contacts` and `/inbox` → `/communications/inbox`.

**D2 — App identity is carried by a layout route, not by string matching in
the root.** Each app has a layout route at its prefix that declares the app
key through TanStack `staticData`. The root finds the active app from the
matched routes. The alternative (matching the pathname against a prefix
list in the root) was rejected: the root is already the busiest file in the
frontend, and a route is a real boundary while a prefix is a convention.

**D3 — Module enablement is injected into the page, not fetched.** The
server already knows the resolved module list and already stamps a runtime
config object into `index.html`. It adds the enabled module names there.
This is available before the first render and before auth, needs no request,
and keeps the backend allowlist as the single source of truth. When the field
is absent (the Vite dev server serves an untemplated `index.html`) the SPA
treats every known module as enabled, so local development is unchanged.

**D4 — Disabled is shown, forbidden is hidden.** Enablement is an
installation property: a disabled app stays in the switcher, greyed, with a
"Not enabled" caption, and its pages render a not-enabled page. Permissions
are a user property: an app the user has no permission for is absent from
the switcher, and the existing permission guard keeps rendering the
forbidden page for deep links. The two never mix.

**D5 — Home is an app, administration is not.** The dashboard is the "Home"
tile, the landing page after sign-in, and the fallback for every not-enabled
and forbidden page. Settings, workspace and system admin have no tile: they
are reached from the avatar menu. Home and the administration pages render
without a sidebar; their in-page navigation (`SettingsLayout` sections)
already covers what a sidebar would.

**D6 — The shell stays data-driven.** `@vantigo/frontend-shell` gets the
switcher, the account menu and the header slots as components that take
plain data. It never learns what "owner", "system admin" or "enabled" mean;
the host filters and marks everything before handing it over. This keeps the
existing rule that composition happens in the host.

## 3. URL map

| App | Prefix | Routes |
|---|---|---|
| Home | `/dashboard` | `/dashboard` (`/` redirects here; system admins still land on `/admin`) |
| Customers | `/customers` | `/customers`, `/customers/$customerId`, `/customers/$customerId/energy`, `/customers/contacts`, `/customers/contacts/$contactId` |
| Communications | `/communications` | `/communications` → redirect to `/communications/inbox`; `/communications/inbox`, `/communications/channels`, `/communications/suppressions` |
| Products | `/products` | `/products`, `/products/$productId`, `/products/categories`, `/products/tax-categories` |
| Energy | `/energy` | `/energy` → redirect to `/energy/metering-points`; `/energy/metering-points`, `/energy/metering-points/$meteringPointId` |
| Administration (no app) | — | `/settings/*`, `/workspace/*`, `/admin/*` unchanged |
| Public (no app) | — | sign-in, setup, password and invitation flows, session-expired unchanged |

`/customers/contacts` sits beside `/customers/$customerId`. TanStack ranks
static segments above dynamic ones and customer ids are UUIDs, so there is no
clash.

## 4. The app registry

New file `apps/host/frontend/src/apps.ts`:

```ts
export type AppKey = "home" | ModuleKey;

export interface AppDefinition {
  key: AppKey;
  /** The backend module that must be enabled; undefined for Home. */
  module?: ModuleKey;
  /** i18n key in the host navigation catalog. */
  label: string;
  icon: NavItem["icon"];
  /** Where the switcher tile navigates. Navigation is untyped (like the
   *  sidebar links), and /dashboard's validator derives its defaults from
   *  an empty query string, so no search object is carried. */
  home: string;
  /** Sidebar sections; empty means the app renders without a sidebar. */
  navSections: readonly NavSection[];
  /** The tile is hidden unless the user holds one of these. */
  requiredPermissions?: readonly string[];
}

export const apps: readonly AppDefinition[]; // switcher order
```

`navigation.ts` keeps `NavItem`, `NavSection`, `moduleKeys`, `ModuleKey`,
`hasPermissions`, `navSearchFor` and `activeNavPath`. Its single global
`navSections` array and the `placement: "lower"` field are removed; sections
now live on the app that owns them. `visibleNavSections` takes the sections
to filter as an argument instead of reading a module-level constant.
Consumers that need every destination (spotlight navigation actions, the
permission guard's prefix rules) read `apps.flatMap((app) => app.navSections)`.

Per-app sections, items moved verbatim from today's catalog. The section
headings ("Customer workspace", "Catalog", …) are dropped: the header already
names the app, so each app has one unlabeled section.

- Home: none.
- Customers: Customers (`/customers`), Contacts (`/customers/contacts`).
- Communications: Inbox (`/communications/inbox`), Channels, Suppressions.
- Products: Products, Categories.
- Energy: Metering points.

Each app's `requiredPermissions` is the union of its items' permissions, so
a tile is shown exactly when at least one of its sidebar entries would be.

### 4.1 Account menu catalog

New file `apps/host/frontend/src/account-menu.ts` holding the entries that
leave the sidebar, grouped:

| Section (i18n label) | Entry | Gate |
|---|---|---|
| Your account | Settings → `/settings` | none |
| Workspace | Workspace admin → `/workspace/overview` | `ownerOnly` |
| Workspace | Roles and access → `/workspace/roles` | `capability: "authorization"` |
| System | System admin → `/admin` | `systemAdminOnly` |

Entries reuse the `NavItem` gating fields. A `visibleAccountMenuSections`
function applies the same rules `visibleNavSections` applies and drops empty
sections. Users, invitations and profile/security stay as in-page tabs.

## 5. Routes

### 5.1 File moves and additions

```
routes/
  dashboard.tsx                       staticData.app = "home"
  customers.tsx                       NEW layout, app = "customers"
  customers/contacts/index.tsx        moved from contacts/index.tsx
  customers/contacts/$contactId.tsx   moved from contacts/$contactId.tsx
  communications.tsx                  NEW layout, app = "communications"
  communications/index.tsx            NEW: redirect → /communications/inbox
  communications/inbox.tsx            moved from inbox.tsx
  products.tsx                        NEW layout, app = "products"
  energy.tsx                          NEW layout, app = "energy"
  energy/index.tsx                    NEW: redirect → /energy/metering-points
```

Everything else is unchanged. `routeTree.gen.ts` is regenerated.

### 5.2 Static route data

A module augmentation in the host makes the key typed:

```ts
declare module "@tanstack/react-router" {
  interface StaticDataRouteOption { app?: AppKey }
}
```

The dashboard route sets `staticData: { app: "home" }`. Each app layout
route sets its own key. Nothing else sets one.

### 5.3 App layout routes

All four are the same small shape, built from one shared helper in
`apps/host/frontend/src/routes/-app-layout.tsx`:

```ts
export const Route = createFileRoute("/customers")({
  staticData: { app: "customers" },
  component: () => <AppLayout app="customers" />,
});
```

`AppLayout` renders `<Outlet />` when the app's module is enabled and
`<ModuleNotEnabledPage app={...} />` otherwise. It is a component check, not
a `beforeLoad` redirect, so the URL stays put and the page can name the app.

`ModuleNotEnabledPage` (new, `apps/host/frontend/src/components/errors.tsx`
alongside `ForbiddenPage`) shows the app label, "This module is not enabled
in this installation. Contact your administrator." and a button back to the
dashboard. Strings go in the host error catalog, en and nb.

The customer detail energy tab (`customers/$customerId.energy.tsx`) also
renders `ModuleNotEnabledPage` inline when energy is disabled, because it
calls the energy API from inside the customers app.

The communications and energy index routes are pure `beforeLoad` redirects,
like `routes/index.tsx` today.

## 6. Shell

All in `packages/frontend-shell/src`.

### 6.1 `AppShellLayout`

Props change from the current shape to:

```ts
interface AppShellLayoutProps {
  /** Shown next to the logo. Defaults to the runtime app title. */
  title?: string;
  /** Header center slot (the search box on desktop). */
  headerCenter?: ReactNode;
  /** Header right slot (mobile search trigger, switcher, account menu). */
  headerActions?: ReactNode;
  /** Sidebar navigation. Omit to render no sidebar and no burger. */
  nav?: (closeMobileNav: () => void) => ReactNode;
  overlay?: (closeMobileNav: () => void) => ReactNode;
  children: ReactNode;
}
```

Removed: `moduleName` (renamed `title`), `user`, `userMenuItems`,
`onSignOut`, `signOutDisabled`, `navbarTop`, `navLower`. The bottom-of-
sidebar account button and its `Menu` go with them. When `nav` is undefined
the Mantine `AppShell` gets no `navbar` prop and the `Burger` is not
rendered, so `AppShell.Main` spans the full width.

Header, left to right: burger (mobile, only with a sidebar), logo, divider,
title; `headerCenter` in a flex-grow group, hidden below `sm`;
`headerActions` right-aligned with `ml="auto"`.

### 6.2 `AppSwitcher`

Restored from the version deleted in `c215e7a`, with a disabled state:

```ts
interface SwitcherApp {
  id: string;
  label: string;
  icon: ComponentType<{ size?: number | string; stroke?: number | string }>;
  onSelect: () => void;
  current?: boolean;
  /** Present when the app is installed but turned off; shown as a caption. */
  disabledReason?: string;
}
```

A round subtle `ActionIcon` with `IconGridDots` (aria-label "Switch app")
opens a `Popover` (`bottom-end`, width 380) containing a 3-column
`SimpleGrid` of tiles: `ThemeIcon` over a 2-line clamped label. The current
tile is filled, `aria-current="true"` and inert. A disabled tile is
`c="dimmed"`, `aria-disabled="true"`, not focusable as a button, and shows
`disabledReason` as a small caption under the label. Selecting a tile calls
`onSelect` and closes the popover.

### 6.3 `AccountMenu`

```ts
interface AccountMenuSection { label: string; items: AccountMenuItem[] }
interface AccountMenuItem { label: string; icon; onSelect: () => void }
interface AccountMenuProps {
  user: ShellUser | undefined;
  sections: readonly AccountMenuSection[];
  onSignOut: () => void;
  signOutDisabled?: boolean;
}
```

Trigger: the `Avatar` with initials (or `avatarUrl`), aria-label "Open
account menu". Dropdown: a non-interactive header with display name and
email, then for each section a `Menu.Label` and its items, with a
`Menu.Divider` between sections, then a divider and the red sign-out item.
Sections arrive already filtered; the component renders what it is given.

### 6.4 Search

`SpotlightSearchBox` is unchanged and is passed as `headerCenter`. A new
`SpotlightSearchButton` (an `ActionIcon` with `IconSearch`, `hiddenFrom="sm"`)
opens the spotlight and is the first item in `headerActions`. mod+K is
unchanged.

### 6.5 Strings

Shell catalog (`i18n/catalogs/shell.ts`), en and nb: `switchApp`,
`appsMenu`, `openSearch`, `openAccountMenu` (exists), `signOut` (exists).
Host navigation catalog gains the Home label, the account menu section
labels and the "Not enabled" caption (the host passes it to the switcher as
`disabledReason`); the host error catalog gains the not-enabled page strings.

## 7. Module enablement

### 7.1 Server

`apps/server/internal/web/index.go`: `runtimeConfig` gains
`Modules []string \`json:"modules"\``, populated from the resolved
`config.Modules`. `NewIndex` gains the module list as a parameter; its one
caller passes `cfg.Modules`. `index_test.go` asserts the list is present in
the injected script and that the CSP hash still matches.

### 7.2 Shell config

`app-config.ts`: `InjectedAppConfig.modules?: string[] | null`;
`AppConfig.modules?: readonly string[]` (undefined when not injected). No
defaulting here: the shell does not know the module names.

### 7.3 Host helper

New `apps/host/frontend/src/lib/enabled-modules.ts`:

```ts
/** Enabled module keys: the injected list intersected with the known keys,
 *  or every known key when nothing was injected (Vite dev server). */
export const enabledModuleKeys = (): readonly ModuleKey[]
```

The three hardcoded `moduleKeys` usages (`__root.tsx`, `dashboard.tsx`,
`customers/-customer-detail-layout.tsx`) call this instead. The
`enabledModules` prop plumbing they already have is kept.

### 7.4 Where the gate bites

| Place | Behaviour when the module is off |
|---|---|
| Switcher tile | shown, muted, "Not enabled" caption, not selectable |
| App layout route | renders `ModuleNotEnabledPage` instead of `<Outlet />` |
| Root | passes no `nav` when the active app is disabled, so the page is sidebar-less |
| Customer detail energy tab | renders `ModuleNotEnabledPage` inline |
| Dashboard cards, spotlight, customer tabs | already filter on the enabled list; now do so for real |

`ModuleAccessGuard` is unchanged: it stays permission-only.

## 8. Host root composition

`RootLayout` in `__root.tsx`:

1. `useMatches()` → the deepest match with `staticData.app` is the active
   app, or undefined on administration and public paths.
2. Builds switcher tiles from `apps`: drop apps failing `hasPermissions`,
   mark `current` for the active app, set `disabledReason` for apps whose
   module is not in `enabledModuleKeys()`. `onSelect` navigates to
   `app.home`.
3. Builds account menu sections with `visibleAccountMenuSections` and the
   same gating inputs the sidebar used.
4. `nav` is `renderNavSections(visibleNavSections(activeApp.navSections, …))`
   when the active app exists, is enabled and has sections; otherwise
   omitted.
5. `title` is the active app's translated label, or undefined so the shell
   falls back to the configured product title.

`renderNavSections`, the maintenance alert, the spotlight and the sign-out
mutation are unchanged. The root's `beforeLoad` is unchanged.

## 9. Links that move

Every reference to `/contacts` and `/inbox` changes. The typed router catches
the typed ones. **The following are untyped (`as never` casts or template
strings) and must be found by grep, not by the compiler:**

- `apps/customers/frontend/src/pages/contacts.$contactId.tsx`
- `apps/customers/frontend/src/pages/contacts.index.tsx`
- `apps/customers/frontend/src/pages/-customer-contacts-card.tsx`
- `apps/customers/frontend/src/components/app-spotlight.tsx` and the
  `test/route-tree.tsx` fixtures in the customers and products packages
- `apps/host/frontend/src/components/app-spotlight.tsx` (quick action paths)
- `apps/host/frontend/src/routes/dashboard.tsx` (activity links)
- `apps/host/frontend/src/routes/customers/-customer-detail-layout.tsx`

The module-package links stay as string updates in place; centralising
module paths is a separate cleanup.

## 10. Documentation

- `CONTRIBUTING.md` "SPA URL convention": replace with the per-app prefix
  rule and the URL map from §3, and describe the app registry as the place
  a new module registers its tile and sidebar.
- `docs/module-boundaries.md` "Adding a module" step 6: add "register the
  app in `apps/host/frontend/src/apps.ts` and add its layout route".

## 11. Testing

Vitest with jsdom, in the package that owns the code.

Shell:
- `AppSwitcher`: renders normal, current and disabled tiles; the current
  tile is `aria-current`; the disabled tile is `aria-disabled`, shows its
  reason and does not call `onSelect`; a normal tile calls `onSelect` and
  closes.
- `AccountMenu`: renders section labels and items in order; empty sections
  are not passed by design, so the test covers one and three sections;
  sign-out calls `onSignOut` and respects `signOutDisabled`.
- `AppShellLayout`: without `nav` renders no `nav` landmark and no burger;
  with `nav` renders both; `headerCenter` and `headerActions` land in the
  header.
- `app-config`: `modules` parsed when injected, undefined when absent.

Host:
- `enabledModuleKeys`: unknown names ignored; absent → all known keys.
- Active app from matches: deepest tagged match wins; none for `/settings`.
- `visibleNavSections` per app and `visibleAccountMenuSections` for a plain
  member, an owner, an authorization manager and a system admin.
- Switcher tile building: permission-less apps absent, disabled apps marked,
  current app marked.
- App layout: renders outlet when enabled, `ModuleNotEnabledPage` when not.
- Redirect routes: `/communications` and `/energy` `beforeLoad` throw the
  expected redirect (same style as `route-guards.test.ts`).

Server:
- `index_test.go`: modules list is stamped and the inline-script hash
  matches the emitted script.

Gates before the PR: regenerate the route tree, `bun run` type-check across
every workspace, frontend lint (module-isolation rule), the workspace test
suites run one at a time, and `go test ./...` with the race detector pinned
to four CPUs.
