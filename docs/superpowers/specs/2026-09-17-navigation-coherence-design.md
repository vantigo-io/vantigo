# Navigation coherence — design

## 1. Scope

Make every page navigate the same way. Today the SPA has four ways of
expressing "where you are within an area": the shell sidebar (module apps),
an in-content side menu (`SettingsLayout`, on `/settings` and `/workspace`),
route-backed tabs (customer detail) and local-state tabs (roles and access).
Page headers are likewise split: module pages use the shared `PageHeader`
while every host page rolls its own title block, and the four detail pages
each hand-roll breadcrumbs that repeat the area name three times (shell
header, breadcrumb root, eyebrow).

Goals, in priority order:

1. One rule per navigation level: sidebar for pages within an area, tabs for
   views of one page, segmented controls for filters only.
2. Settings, workspace administration and system administration use the
   same shell sidebar as the module apps. `SettingsLayout` goes away.
3. Every page renders the same header, and detail pages render the same
   breadcrumbs, from one shared component.
4. Every tab is in the URL.

Explicitly **not** in scope:

- Page width. Host pages keep their `maw` wrappers where they have them;
  a page's content width is a page decision, not a navigation one.
- The app switcher. Settings, workspace and admin still have no tile (spec
  D5 of the app-switcher design stands: they are reached from the avatar
  menu).
- Any backend change.

## 2. Inventory

| Where | Today | After |
|---|---|---|
| Customers, Communications, Products, Energy | Shell sidebar from the registry | Unchanged. Products gains a Tax categories entry (the route exists but nothing links to it). |
| `/settings/*` | In-content side menu (Profile, Security); mobile `Select` that reloads the page | Shell sidebar: Profile, Security. `/settings` redirects to `/settings/profile`. |
| `/workspace/*` | In-content side menu (Overview, Users, Invitations, Roles) | Shell sidebar with the same four entries, each gated as its route is. `/workspace` redirects to `/workspace/overview`. |
| `/admin` | Sidebar-less single page | Unchanged layout (one page, no sidebar, like Home); header names the area. |
| Customer detail | Tabs: Overview, Energy, Correspondence (jumps to the inbox) | Tabs: Overview, Energy. Correspondence becomes an "Open in inbox" header action. |
| Roles and access | Local-state tabs | Tabs driven by a `section` search param. |
| Dashboard, metering point, timeline, users modal | Segmented controls | Unchanged: they are filters or form modes. |
| Host page titles | Ad-hoc `Title` blocks, some with icons | `PageHeader`, eyebrow = area name. |
| Detail page breadcrumbs | Hand-rolled `Breadcrumbs` + eyebrow | `PageHeader` `breadcrumbs`; no eyebrow. |

## 3. Decisions

**D1 — Areas join the registry.** `apps.ts` gains `areas`: Settings,
Workspace and System admin, each with a key, an i18n label, a home path and
sidebar sections in the same `NavSection` shape the module apps use. They
are not in `apps`, so the switcher never shows them, but the root resolves
the active area exactly as it resolves the active app: the layout route at
`/settings`, `/workspace` and `/admin` carries `staticData.app`. The root's
helpers (`activeAppKey`, `appNavSections`, `appTitleLabel`) accept either
an app or an area. An area has no module, so it is always enabled; its
sidebar entries carry the same visibility flags the account menu uses
(`ownerOnly`, `capability`), so the Owner-only workspace entries and the
capability-gated roles entry filter themselves.

**D2 — The `/workspace` layout admits anyone who may see one of its
pages.** Today the layout's `beforeLoad` requires Owner, so a non-Owner who
holds the authorization capability is bounced from `/workspace/roles` even
though the account menu offers it and the roles route itself admits them.
The layout gate becomes "Owner or `canManageAuthorization`"; each child
keeps its own gate, so nothing that was closed opens.

**D3 — Route files own their page components.** `ProfileTab` and
`SecurityTab` currently live in the `/settings` layout route file and are
imported by the child route files. They move into the child route files
(`settings/profile.tsx`, `settings/security.tsx`), matching how the
workspace pages are laid out. The layout route renders `Outlet`.

**D4 — `PageHeader` grows breadcrumbs and drops nothing.** It gains
`breadcrumbs?: readonly { label: ReactNode; to?: string }[]`, rendered above
the title in place of the eyebrow; the last crumb has no `to` and is the
current page. A page passes either `eyebrow` (top-level pages: the area
name) or `breadcrumbs` (detail pages: area list page, then the entity),
never both. The shell does not depend on the router, so links render
through a `linkComponent` the root hands to `AppShellLayout`, provided via
context; without one, crumbs render as plain anchors. Page titles are text
plus optional badges; the icons some pages put in the title go, since the
sidebar already carries the area's icon.

**D5 — `PageTabs` in the shell.** A thin wrapper over Mantine `Tabs` that
renders only the list, from `{ value, label, icon? }[]`, with `value` and
`onChange` controlled by the caller. It exists so the placement (directly
under the header) and the style are decided once. Customer detail derives
the value from the matched route; roles from the `section` search param.

**D6 — Correspondence is an action, not a tab.** A tab that leaves the page
can never be the active tab. The host layout passes an "Open in inbox"
button into `CustomerDetailHeader`'s new `actions` slot, gated exactly as
the tab was (communications enabled and `communications:conversations-view`).
The customers package learns nothing about communications.

**D7 — Tax categories joins the Products sidebar.** Entry after Categories,
gated on `products:tax-categories-view`.

## 4. Components

### Shell (`packages/frontend-shell`)

- `page-header.tsx`: `breadcrumbs` prop; reads the link component from
  `link-context.tsx`.
- `link-context.tsx`: `ShellLinkProvider` and `useShellLink`. The provider
  takes any component accepting `to` and `children`.
- `app-shell-layout.tsx`: `linkComponent?` prop, wraps children in the
  provider.
- `page-tabs.tsx`: `PageTabs`.
- `index.ts` exports the above.

### Host (`apps/host/frontend/src`)

- `apps.ts`: `AreaKey`, `AreaDefinition`, `areas`, `areaForKey`; the
  `staticData.app` augmentation widens to `AppKey | AreaKey`; `activeAppKey`
  returns the widened type; `appNavSections` and `appTitleLabel` take
  `AppDefinition | AreaDefinition`.
- `routes/settings.tsx`, `routes/workspace.tsx`, `routes/admin.tsx`: declare
  `staticData: { app: "<area>" }`, render `Outlet`.
- `routes/settings/index.tsx`, `routes/workspace/index.tsx`: redirects.
- `routes/settings/profile.tsx`, `routes/settings/security.tsx`: own their
  page components, render `PageHeader`.
- `routes/workspace/{overview,users,invitations,roles}.tsx`: `PageHeader`;
  roles uses `PageTabs` with `validateSearch` for `section`.
- `routes/admin/index.tsx`, `routes/dashboard.tsx`: `PageHeader`.
- `routes/customers/-customer-detail-layout.tsx`: `PageTabs`, correspondence
  action, `visibleCustomerDetailTabs` keeps its signature but returns only
  route tabs; a sibling `showCorrespondenceAction` uses the same predicate.
- `routes/__root.tsx`: passes `linkComponent={Link}`; resolves areas.
- `components/settings-layout.tsx`: deleted.
- Catalogs: new area labels and page titles; `systemAdmin.settingsSection`
  removed.

### Module packages

- `customers-ui`: `CustomerDetailHeader` gains `actions?: ReactNode`;
  breadcrumbs move into `PageHeader` on customer and contact detail.
- `energy-ui`, `products-ui`: breadcrumbs move into `PageHeader` on the
  detail pages; title icons removed.

### Docs

CONTRIBUTING's frontend section states the rules: sidebar for pages within
an area (always the shell's), tabs for views of one page (always in the
URL, always `PageTabs` under the header), segmented controls for filters,
`PageHeader` on every page with eyebrow or breadcrumbs.

## 5. Behaviour to preserve

- The MFA enrolment gate: the root passes no sidebar while gated, so the
  held administrator still sees only the security page. The header now
  reads "Settings" rather than the product title.
- Deep links into `/settings/*` and `/workspace/*` behave as today.
- The account menu, spotlight and permission guard are unchanged; they read
  `accountMenuSections` and `allNavSections`, neither of which gains the
  area entries (the spotlight already lists the account menu's
  destinations).

## 6. Testing

- `apps.test.ts`: areas resolve by key, are never switcher tiles, their
  sidebar entries filter by owner and capability, `activeAppKey` reads an
  area from `staticData`, the Products path list includes tax categories.
- `route-guards.test.ts`: `/workspace` admits a non-Owner with the
  authorization capability and still bounces a plain member; the two new
  index routes redirect.
- `customer-detail-tabs.test.ts` and `customer-detail-route.test.tsx`: the
  tab row is Overview and Energy; the inbox action appears exactly when the
  correspondence tab did.
- Shell: `page-header.test.tsx` (eyebrow, breadcrumbs with and without a
  link component), `page-tabs.test.tsx` (renders items, reports changes).
- A roles route test pins the `section` search param default and rejects an
  unknown value.
- Existing suites, typecheck, lint, format and translations checks stay
  green.
