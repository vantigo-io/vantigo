# Navigation Coherence Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Every page navigates the same way: the shell sidebar for pages within an area (now including Settings, Workspace and System admin), URL-backed `PageTabs` for views of one page, and one `PageHeader` with an eyebrow or breadcrumbs on every page.

**Architecture:** The host's app registry (`apps.ts`) gains *areas* alongside apps; their layout routes tag the subtree with `staticData.app` exactly as module apps do, so the root's existing sidebar and header logic covers them and `SettingsLayout` is deleted. The shell package gains `PageTabs`, a `breadcrumbs` prop on `PageHeader`, and a link-component context so it stays router-agnostic. Customer detail loses its page-leaving Correspondence tab in favour of a header action.

**Tech Stack:** React 19, TanStack Router 1.170 (file-based, `autoCodeSplitting`), Mantine 9, Vitest 4 + jsdom + Testing Library, Bun workspaces.

**Spec:** `docs/superpowers/specs/2026-09-17-navigation-coherence-design.md`

## Global Constraints

- Every user-visible string goes through i18n with `en` and `nb` entries; the pre-commit hook runs `bun run translations:check`, which also rejects hardcoded JSX text/`label`/`aria-label`/`title`/`placeholder`/`description` literals outside test and catalog files.
- `@vantigo/frontend-shell` does not depend on `@tanstack/react-router`; it takes plain data and an optional link component.
- Module frontend packages never import the host or each other.
- Route files under `apps/host/frontend/src/routes/` prefixed with `-` are ignored by the router; `routeTree.gen.ts` is generated (on `vite build` and `vitest run`), never edited by hand.
- Settings, workspace and admin get no app-switcher tile.
- Page widths (`maw` wrappers) stay as they are.
- Commit messages follow `type(scope): summary` and end with the attribution lines from the session's system reminder.
- Branch: `feat/navigation-coherence` (spec committed).

### Commands used throughout

```bash
bun run --cwd packages/frontend-shell test                 # shell unit tests
bun run --cwd apps/host/frontend test                      # host unit tests (add a path to run one file)
bun run --cwd apps/customers/frontend test                 # module package tests (same for energy, products)
bun run frontend:typecheck
bun run frontend:lint
bun run format:check          # biome; `bun run format:write` fixes
bun run translations:check
bun run frontend:test
```

---

## File structure

**Shell (`packages/frontend-shell/src`)**
- Create `link-context.tsx` — `ShellLinkProvider`, `useShellLink`, `ShellLinkComponent`.
- Modify `page-header.tsx` — `breadcrumbs` prop; create `page-header.test.tsx`.
- Create `page-tabs.tsx`, `page-tabs.test.tsx`.
- Modify `app-shell-layout.tsx` — `linkComponent` prop wrapping children in the provider.
- Modify `index.ts` — exports.

**Host (`apps/host/frontend/src`)**
- Modify `apps.ts` — `AreaKey`, `AreaDefinition`, `areas`, `areaForKey`, widened helpers; `apps.test.ts`.
- Modify `routes/__root.tsx` — resolve areas, pass `linkComponent`.
- Modify `routes/settings.tsx`, `routes/workspace.tsx`, `routes/admin.tsx` — `staticData`, `Outlet`.
- Create `routes/settings/index.tsx`, `routes/workspace/index.tsx` — redirects.
- Modify `routes/settings/profile.tsx`, `routes/settings/security.tsx` — own their components, `PageHeader`.
- Modify `routes/workspace/overview.tsx`, `users.tsx`, `invitations.tsx`, `roles.tsx` — `PageHeader`; roles gets `PageTabs` + `section` search.
- Modify `routes/admin/index.tsx`, `routes/dashboard.tsx` — `PageHeader`.
- Modify `routes/customers/-customer-detail-layout.tsx` and its two tests.
- Delete `components/settings-layout.tsx`.
- Modify `catalogs/navigation.ts`, `catalogs/system-admin.ts`, `catalogs/settings.ts` or `packages/frontend-shell/src/i18n/catalogs/settings.ts` (whichever holds the profile/security strings), `catalogs/admin.ts` as needed.
- Modify `routes/route-guards.test.ts`.

**Module packages**
- `apps/customers/frontend/src/pages/customers.$customerId.tsx` — `actions` slot, breadcrumbs via `PageHeader`.
- `apps/customers/frontend/src/pages/contacts.$contactId.tsx`, `apps/energy/frontend/src/pages/metering-points.$meteringPointId.tsx`, `apps/products/frontend/src/pages/products.$productId.tsx`, `apps/products/frontend/src/pages/categories.tsx` — breadcrumbs via `PageHeader`, title icons removed.

**Docs** — `CONTRIBUTING.md` frontend section.

---

### Task 1: Shell link context and `PageHeader` breadcrumbs

**Files:**
- Create: `packages/frontend-shell/src/link-context.tsx`
- Modify: `packages/frontend-shell/src/page-header.tsx`
- Modify: `packages/frontend-shell/src/app-shell-layout.tsx`
- Modify: `packages/frontend-shell/src/index.ts`
- Test: `packages/frontend-shell/src/page-header.test.tsx`

**Interfaces:**
- Produces: `type ShellLinkComponent = ComponentType<{ to: string; children?: ReactNode }>`; `ShellLinkProvider({ link, children })`; `useShellLink(): ShellLinkComponent | undefined`; `PageHeaderProps.breadcrumbs?: readonly PageBreadcrumb[]` with `PageBreadcrumb = { label: ReactNode; to?: string }`; `PageHeaderProps.eyebrow` becomes optional; `AppShellLayoutProps.linkComponent?: ShellLinkComponent`.

- [ ] **Step 1: Write the failing tests**

```tsx
// page-header.test.tsx
import { MantineProvider } from "@mantine/core";
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { ShellLinkProvider } from "./link-context";
import { PageHeader } from "./page-header";

const wrap = (ui: React.ReactNode) => render(<MantineProvider env="test">{ui}</MantineProvider>);

describe("PageHeader", () => {
  it("renders the eyebrow, title and description", () => {
    wrap(<PageHeader eyebrow="Customers" title="All customers" description="Everyone you bill." />);
    expect(screen.getByText("Customers")).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "All customers" })).toBeInTheDocument();
    expect(screen.getByText("Everyone you bill.")).toBeInTheDocument();
  });

  it("renders breadcrumbs as plain anchors without a link component, the last one as text", () => {
    wrap(<PageHeader title="Acme" breadcrumbs={[{ label: "Customers", to: "/customers" }, { label: "Acme" }]} />);
    const crumb = screen.getByRole("link", { name: "Customers" });
    expect(crumb).toHaveAttribute("href", "/customers");
    expect(screen.getAllByText("Acme")).toHaveLength(2); // crumb + title
    expect(screen.queryByRole("link", { name: "Acme" })).not.toBeInTheDocument();
  });

  it("renders breadcrumb links through the provided link component", () => {
    const FakeLink = ({ to, children }: { to: string; children?: React.ReactNode }) => (
      <a href={`#fake${to}`}>{children}</a>
    );
    wrap(
      <ShellLinkProvider link={FakeLink}>
        <PageHeader title="Acme" breadcrumbs={[{ label: "Customers", to: "/customers" }, { label: "Acme" }]} />
      </ShellLinkProvider>,
    );
    expect(screen.getByRole("link", { name: "Customers" })).toHaveAttribute("href", "#fake/customers");
  });
});
```

- [ ] **Step 2: Run to verify failure** — `bun run --cwd packages/frontend-shell test src/page-header.test.tsx`; expected: cannot resolve `./link-context`.

- [ ] **Step 3: Implement**

```tsx
// link-context.tsx
import { type ComponentType, createContext, type ReactNode, useContext } from "react";

/** A client-side link: whatever router the host uses, given `to` and children. */
export type ShellLinkComponent = ComponentType<{ to: string; children?: ReactNode }>;

const ShellLinkContext = createContext<ShellLinkComponent | undefined>(undefined);

export const ShellLinkProvider = ({ link, children }: { link: ShellLinkComponent; children: ReactNode }) => (
  <ShellLinkContext.Provider value={link}>{children}</ShellLinkContext.Provider>
);

/** The host's link component, or undefined when none was provided (plain anchors then). */
export const useShellLink = () => useContext(ShellLinkContext);
```

`page-header.tsx`: add `breadcrumbs`, make `eyebrow` optional, render `<Breadcrumbs>` above the title when given; a crumb with `to` renders `<Anchor component={Link} to={to} size="sm">` (or `<Anchor href={to} size="sm">` without a provider), one without renders `<Text size="sm">`. Doc comment: pass `eyebrow` on top-level pages and `breadcrumbs` on detail pages, never both.

`app-shell-layout.tsx`: `linkComponent?: ShellLinkComponent`; wrap the whole `AppShell` in `ShellLinkProvider` when given.

`index.ts`: export `ShellLinkProvider`, `useShellLink`, `type ShellLinkComponent`, `type PageBreadcrumb`.

- [ ] **Step 4: Run tests** — shell suite green.
- [ ] **Step 5: Commit** — `feat(shell): breadcrumbs on PageHeader through a host-provided link component`.

---

### Task 2: Shell `PageTabs`

**Files:**
- Create: `packages/frontend-shell/src/page-tabs.tsx`, `packages/frontend-shell/src/page-tabs.test.tsx`
- Modify: `packages/frontend-shell/src/index.ts`

**Interfaces:**
- Produces: `PageTabs<Value extends string>({ items: readonly { value: Value; label: ReactNode; icon?: ComponentType<{ size?: number }> }[], value: Value, onChange: (value: Value) => void, "aria-label"?: string, children?: ReactNode })`. Renders Mantine `Tabs` with only the list (children, if any, are the caller's `Tabs.Panel`s).

- [ ] **Step 1: Failing test** — renders the items as tabs, marks `value` selected, calls `onChange` with the clicked value, hides nothing when given one item (the caller decides whether to render).
- [ ] **Step 2: Verify failure.**
- [ ] **Step 3: Implement** — `Tabs value onChange={(v) => v && onChange(v as Value)} keepMounted={false}`; `Tabs.List aria-label`; `Tabs.Tab leftSection={<Icon size={16} />}`.
- [ ] **Step 4: Run tests.**
- [ ] **Step 5: Commit** — `feat(shell): add PageTabs for URL-backed views of one page`.

---

### Task 3: Areas in the registry, resolved by the root

**Files:**
- Modify: `apps/host/frontend/src/apps.ts`, `apps/host/frontend/src/apps.test.ts`
- Modify: `apps/host/frontend/src/catalogs/navigation.ts` (labels `navigation.profile`, `navigation.security`, `navigation.taxCategories`; reuse `navigation.settings`, `navigation.workspaceAdmin`, `navigation.systemAdmin`, `navigation.rolesAccess`; add `navigation.overview`, `navigation.users`, `navigation.invitations`)
- Modify: `apps/host/frontend/src/routes/settings.tsx`, `routes/workspace.tsx`, `routes/admin.tsx`
- Create: `apps/host/frontend/src/routes/settings/index.tsx`, `routes/workspace/index.tsx`
- Modify: `apps/host/frontend/src/routes/__root.tsx`
- Modify: `apps/host/frontend/src/routes/route-guards.test.ts`
- Delete: `apps/host/frontend/src/components/settings-layout.tsx` (its consumers are rewritten in Tasks 4)

**Interfaces:**
- Produces in `apps.ts`:
  ```ts
  export type AreaKey = "settings" | "workspace" | "admin";
  export interface AreaDefinition { key: AreaKey; label: string; home: string; navSections: readonly NavSection[] }
  export const areas: readonly AreaDefinition[];
  export const areaForKey: (key: AppKey | AreaKey) => AppDefinition | AreaDefinition;  // throws on unknown
  export const isArea: (value: AppDefinition | AreaDefinition) => value is AreaDefinition;
  export const activeAppKey: (matches) => AppKey | AreaKey | undefined;
  export const appNavSections: (area: AppDefinition | AreaDefinition | undefined, enabledModules, visibility) => NavSection[];
  export const appTitleLabel: (area: AppDefinition | AreaDefinition | undefined) => string | undefined;
  ```
  `StaticDataRouteOption.app?: AppKey | AreaKey`. Products sidebar gains `{ label: "navigation.taxCategories", to: "/products/tax-categories", icon: IconReceiptTax, requiredPermissions: ["products:tax-categories-view"] }`.
- Area sidebar entries: settings → `/settings/profile` (IconUser), `/settings/security` (IconShieldLock); workspace → `/workspace/overview` (IconLayoutDashboard, ownerOnly), `/workspace/users` (IconUsers, ownerOnly), `/workspace/invitations` (IconUserPlus, ownerOnly), `/workspace/roles` (IconShieldCheck, capability: "authorization"); admin → no sections.

- [ ] **Step 1: Failing tests in `apps.test.ts`**
  - "declares the three administration areas, none of them a switcher tile": `areas.map(a => a.key)` equals `["settings","workspace","admin"]`; `switcherTiles(["*"], allModules, "settings").map(t => t.app.key)` unchanged and none current.
  - "keeps every area destination under the area's prefix" (same loop as apps).
  - "filters workspace entries by owner and capability": `appNavSections(areaForKey("workspace"), allModules, { permissions: [], isOwner: false, canManageAuthorization: true, enabledModules: allModules })` → only `/workspace/roles`; with `isOwner: true, canManageAuthorization: false` → overview, users, invitations.
  - "names the area in the header": `appTitleLabel(areaForKey("settings"))` is `"navigation.settings"`.
  - "reads an area from staticData": `activeAppKey([{ staticData: { app: "workspace" } }])` is `"workspace"`.
  - Update the bare-path list to include `/products/tax-categories`.
- [ ] **Step 2: Verify failure.**
- [ ] **Step 3: Implement `apps.ts`.** Keep `apps` and `switcherTiles` untouched; `appForKey` stays for apps; add `areaForKey` searching `[...apps, ...areas]`; `isAppEnabled(area)` returns true for an area (`module === undefined` already does).
- [ ] **Step 4: Layout routes.** `settings.tsx`: `createFileRoute("/settings")({ staticData: { app: "settings" }, beforeLoad (session fetch as today), component: Outlet })`. `workspace.tsx`: `staticData: { app: "workspace" }`, gate `if (!isOwner && !access.canManageAuthorization) redirect("/")` where `access` comes from `queryClient.fetchQuery({ queryKey: ["authorization","me"], queryFn: getAuthorizationMe })` (only fetched when not Owner). `admin.tsx`: `staticData: { app: "admin" }`. Index routes: `settings/index.tsx` redirects to `/settings/profile`; `workspace/index.tsx` redirects to `/workspace/overview` (pattern of `communications/index.tsx`).
- [ ] **Step 5: Root.** `const active = activeKey ? areaForKey(activeKey) : undefined;` feed `appNavSections(active, …)` and `appTitleLabel(active)`; `AppShellLayout linkComponent={Link as ShellLinkComponent}`. Switcher `current` compares against `activeKey` as before (never matches an area).
- [ ] **Step 6: Route guard tests.** Add: "/workspace admits a non-Owner with the authorization capability" and "still redirects a member without it"; remove `/workspace` from `ownerGatedRoutes` (children stay); "/settings redirects to the profile page"; "/workspace redirects to the overview".
- [ ] **Step 7: Run host tests, typecheck.** Expect `settings.tsx`/`workspace.tsx` consumers of `SettingsLayout` gone; `ProfileTab`/`SecurityTab` temporarily still exported from `settings.tsx`? No — Task 4 moves them; do Tasks 3 and 4 in one commit if the tree does not compile between them, otherwise commit here.
- [ ] **Step 8: Commit** — `feat(frontend): settings, workspace and admin become sidebar areas in the registry`.

---

### Task 4: Settings and workspace pages on `PageHeader`

**Files:**
- Modify: `apps/host/frontend/src/routes/settings/profile.tsx`, `routes/settings/security.tsx` — receive `ProfileTab`/`SecurityTab` bodies from `routes/settings.tsx`, renamed `ProfilePage`/`SecurityPage`, each wrapped: `<Stack maw={1180} mx="auto" gap="xl"><PageHeader eyebrow={t("navigation.settings")} title={t("profile")} description={t("profileDescription")} />…</Stack>` (add `profileDescription`/`securityDescription` to the settings catalog in `en`/`nb`).
- Modify: `routes/workspace/overview.tsx` (eyebrow `navigation.workspaceAdmin`, title `admin.dashboard`, description `admin.overview`), `users.tsx`, `invitations.tsx`, `roles.tsx` — replace the `Group/Title/Text` blocks with `PageHeader` (actions = the existing right-hand button), drop the title icons.
- Modify: `routes/workspace/roles.tsx` — `validateSearch: (s) => ({ section: isSection(s.section) ? s.section : undefined })` typed `{ section?: RolesSection }`; `const section = Route.useSearch().section ?? "roles"`; `PageTabs items=[roles, assignments, delegations] value={section} onChange={(v) => navigate({ search: { section: v } })}`; keep the `Tabs.Panel`s as `PageTabs` children. Test in `route-guards.test.ts` or a new `workspace/roles-search.test.ts`: `Route.options.validateSearch({ section: "delegations" })` → `{ section: "delegations" }`; `({ section: "bogus" })` → `{ section: undefined }`.
- Remove `systemAdmin.settingsSection`, `systemAdmin.settings`, `systemAdmin.manageWorkspace`, `systemAdmin.overview/users/invitations/roles` from `catalogs/system-admin.ts` if no longer referenced (grep first).

- [ ] Steps: write the search test → fail → implement roles → pass; rewrite the other pages; `bun run --cwd apps/host/frontend test`; `bun run translations:check`; commit `refactor(frontend): settings and workspace pages render the shared page header`.

---

### Task 5: Admin and dashboard headers

**Files:**
- Modify: `apps/host/frontend/src/routes/admin/index.tsx` — `PageHeader eyebrow={t("navigation.systemAdmin")} title={t("systemAdmin.controlPlane")} description={t("systemAdmin.description")}`.
- Modify: `apps/host/frontend/src/routes/dashboard.tsx:469-474` — `PageHeader eyebrow={t("navigation.home")} title={t("dashboard.title")} description={greeting} actions={<Group>…SegmentedControl…DatePickerInput…</Group>}`.
- Check `routes/admin/index.test.tsx` for title assertions and update.

- [ ] Steps: change → run host tests → commit `refactor(frontend): admin and dashboard render the shared page header`.

---

### Task 6: Customer detail tabs and the inbox action

**Files:**
- Modify: `apps/customers/frontend/src/pages/customers.$customerId.tsx` — `CustomerDetailHeader({ customerId, actions })`: the existing badges/edit button group gains `{actions}` appended; breadcrumbs move into `PageHeader` (`breadcrumbs={[{ label: t("customers"), to: "/customers" }, { label: customer.name }]}`, no eyebrow).
- Modify: `apps/host/frontend/src/routes/customers/-customer-detail-layout.tsx` — tabs = overview, energy only; `export const customerDetailLinks = [{ value: "correspondence", … }]` filtered by the same predicate through `visibleCustomerDetailLinks`; render `PageTabs` when `visibleTabs.length > 1`; pass `actions={showInbox && <Button component={Link} to="/communications/inbox" search={{ customerId, … }} variant="default" leftSection={<IconMessages size={16} />}>{t("customer.openInInbox")}</Button>}` (add `customer.openInInbox` en/nb to `catalogs/customer.ts`).
- Modify tests: `customer-detail-tabs.test.ts` (tabs → `["overview","energy"]`; new cases for `visibleCustomerDetailLinks`), `customer-detail-route.test.tsx` (expects the "Open in inbox" link, not a Correspondence tab; the single-tab case hides the tab row but keeps the action).

- [ ] Steps: update tests → fail → implement → pass in host and customers suites → commit `refactor(frontend): customer correspondence is a header action, not a tab`.

---

### Task 7: Detail-page breadcrumbs and title icons in module packages

**Files:**
- Modify: `apps/customers/frontend/src/pages/contacts.$contactId.tsx` (`breadcrumbs=[{contacts,/customers/contacts},{name}]`), `apps/energy/frontend/src/pages/metering-points.$meteringPointId.tsx` (`[{meteringPoints,/energy/metering-points},{gsrn}]`, drop `IconBolt` from the title), `apps/products/frontend/src/pages/products.$productId.tsx` (`[{navigation.products,/products},{product.name}]`, drop `IconPackage`), `apps/products/frontend/src/pages/categories.tsx` (drop `IconCategory`). Remove the `Breadcrumbs`/`Anchor`/`Link` imports that become unused.
- Run each package's tests (`-customer-details.test.tsx`, `-products.productId.test.tsx`, etc.) and adjust any assertion on the removed markup.

- [ ] Steps: change → `bun run --cwd apps/<pkg>/frontend test` ×3 → `bun run frontend:lint` → commit `refactor(frontend): detail pages render breadcrumbs through PageHeader`.

---

### Task 8: Docs and the full gate

**Files:**
- Modify: `CONTRIBUTING.md:142-166` — after the URL convention paragraph, add a "Navigation" paragraph with the four rules (sidebar = pages within an area, always the shell's, declared in `apps.ts` for apps and areas; tabs = views of one page, always in the URL, always `PageTabs` under the header; segmented controls = filters and form modes only; `PageHeader` on every page, `eyebrow` on top-level pages, `breadcrumbs` on detail pages). Replace "`/settings`, `/workspace` and `/admin` are administration pages reached from the avatar menu and belong to no app" with "…are *areas*: declared in `apps.ts` like apps, with their own sidebar, but without a switcher tile".
- Modify: `docs/module-boundaries.md:99-104` only if the registry instructions changed (they did not).

- [ ] Run: `bun run frontend:typecheck && bun run frontend:lint && bun run format:check && bun run translations:check && bun run frontend:test`.
- [ ] Commit `docs(frontend): state the navigation rules`.
- [ ] Push, open the PR with a summary of the inventory and decisions.

## Self-review

- Spec §2 rows → Tasks 3 (settings, workspace, admin, tax categories), 6 (customer detail), 4 (roles), 4–5 (host headers), 6–7 (breadcrumbs). Segmented controls untouched. ✔
- Spec D2 → Task 3 step 4 and step 6. D3 → Task 4. D4 → Task 1. D5 → Task 2. D6 → Task 6. D7 → Task 3. ✔
- Spec §5 MFA gate: root still passes `nav` only when not gated — no task changes that line. ✔
- Names: `areaForKey`, `areas`, `AreaKey`, `PageTabs`, `PageBreadcrumb`, `ShellLinkComponent`, `visibleCustomerDetailLinks` used consistently above. ✔
