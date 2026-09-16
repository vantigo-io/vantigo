# App Switcher and Per-App Shell Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Present each business module as its own "app" with its own URL prefix and sidebar, switched from a waffle menu in the header, with the user menu in an avatar beside it and disabled modules shown as "not enabled".

**Architecture:** One SPA, one TanStack router. A host-side registry (`apps.ts`) declares every app; a layout route per app tags its subtree with the app key via TanStack `staticData` and gates on module enablement. The shell package gains three data-driven components (switcher, account menu, header slots) and learns nothing about permissions or modules. Module enablement is injected into `index.html` by the Go server from the resolved `MODULES` allowlist.

**Tech Stack:** React 19, TanStack Router 1.170 (file-based, `autoCodeSplitting`), Mantine 9, Vitest 4 + jsdom + Testing Library, Bun workspaces, Go 1.x server.

**Spec:** `docs/superpowers/specs/2026-09-16-app-switcher-design.md`

## Global Constraints

- Nothing is in production: URLs move without redirects or compatibility shims.
- No per-app theming; the shared Mantine theme is untouched.
- No backend module renames; URL prefix = backend module name = `MODULES` entry.
- `@vantigo/frontend-shell` components take plain data; the host does all gating.
- Every user-visible string goes through i18n with `en` and `nb` entries (the pre-commit hook runs `bun run translations:check`, which also rejects hardcoded JSX text/`label`/`aria-label`/`title`/`placeholder`/`description` literals outside test and catalog files).
- Module frontend packages never import the host or each other (`no-restricted-imports` lint rule).
- Route files under `apps/host/frontend/src/routes/` are thin; files prefixed with `-` are ignored by the router; `routeTree.gen.ts` is generated, never edited by hand. The generator runs whenever Vite loads the config, i.e. on `vite build` and on `vitest run`.
- Commit messages follow the repo convention `type(scope): summary` and end with the attribution lines given in the session's system reminder.
- Branch: `feat/app-switcher` (already exists, spec committed).

### Commands used throughout

```bash
# host unit tests (one file or all)
bun run --cwd apps/host/frontend test src/apps.test.ts
bun run --cwd apps/host/frontend test
# shell unit tests
bun run --cwd packages/frontend-shell test
# regenerate the route tree (also happens on any host vitest run)
(cd apps/host/frontend && bunx vite build)
# whole-repo gates
bun run frontend:typecheck
bun run frontend:lint
bun run format:check          # biome; `bun run format:write` fixes
bun run translations:check
bun run frontend:test         # every workspace, one at a time
(cd apps/server && go test ./...)
```

---

## File structure

**Server**
- Modify `apps/server/internal/web/index.go` — inject `modules` into the runtime config.
- Modify `apps/server/internal/web/index_test.go`, `apps/server/cmd/vantigo/main.go`, `apps/server/internal/modtest/modtest.go` — new `NewIndex` parameter.

**Shell (`packages/frontend-shell`)**
- Modify `src/app-config.ts` — parse `modules`.
- Create `vitest.config.ts`, `src/test/setup.ts` — jsdom + browser API mocks for component tests.
- Create `src/app-switcher.tsx` — waffle popover with tiles.
- Create `src/account-menu.tsx` — avatar menu with grouped sections; owns `ShellUser`.
- Create `src/spotlight-search-button.tsx` — mobile search trigger.
- Modify `src/app-shell-layout.tsx` — header slots, optional navbar, no account button.
- Modify `src/i18n/catalogs/shell.ts`, `src/index.ts`, `src/spotlight-search-box.tsx` (doc comment).

**Host (`apps/host/frontend/src`)**
- Create `lib/enabled-modules.ts` — `enabledModuleKeys()`.
- Create `apps.ts` — the app registry, `staticData` augmentation, `activeAppKey`, `switcherTiles`.
- Create `account-menu.ts` — grouped avatar-menu catalog.
- Modify `navigation.ts` — drop the global list, `visibleNavSections(sections, ctx)`.
- Create `components/errors/module-not-enabled.tsx`; modify `components/errors/index.ts`.
- Create `routes/-app-layout.tsx`; create layout routes `routes/customers.tsx`, `routes/communications.tsx`, `routes/products.tsx`, `routes/energy.tsx`; create redirect routes `routes/communications/index.tsx`, `routes/energy/index.tsx`.
- Move `routes/contacts/*` → `routes/customers/contacts/*`; move `routes/inbox.tsx` → `routes/communications/inbox.tsx`.
- Modify `routes/__root.tsx`, `routes/dashboard.tsx`, `routes/customers/-customer-detail-layout.tsx`, `routes/customers/$customerId.energy.tsx`, `components/module-access-guard.tsx`, `components/app-spotlight.tsx`, catalogs `navigation.ts` and `error.ts`.

**Module packages** — string updates only: `apps/customers/frontend/src/pages/contacts.$contactId.tsx`, `pages/contacts.index.tsx`, `pages/-customer-contacts-card.tsx`, `components/app-spotlight.tsx`, `test/route-tree.tsx`; `apps/customers/frontend/src/components/app-shell-whitelabel.test.tsx` and `apps/products/frontend/src/components/app-shell-whitelabel.test.tsx` (new shell props).

**Docs** — `CONTRIBUTING.md`, `docs/module-boundaries.md`.

---

### Task 1: Server injects the enabled module list

**Files:**
- Modify: `apps/server/internal/web/index.go`
- Modify: `apps/server/internal/web/index_test.go`
- Modify: `apps/server/cmd/vantigo/main.go:369`
- Modify: `apps/server/internal/modtest/modtest.go:326`

**Interfaces:**
- Produces: `func NewIndex(assets fs.FS, basePath string, b config.Branding, modules []string) (*Index, error)`; the injected object gains `"modules":[...]` (always an array, `[]` when empty or nil).

- [ ] **Step 1: Write the failing tests**

In `apps/server/internal/web/index_test.go`, change the helper and the default-branding expectation, and add a modules test:

```go
func renderIndex(t *testing.T, basePath string, b config.Branding, html string) (*Index, string) {
	t.Helper()
	idx, err := NewIndex(fstest.MapFS{"index.html": {Data: []byte(html)}}, basePath, b, nil)
	if err != nil {
		t.Fatalf("NewIndex: %v", err)
	}
	return idx, string(idx.HTML)
}
```

```go
func TestNewIndex_DefaultBrandingInjectsNulls(t *testing.T) {
	_, html := renderIndex(t, "", config.Branding{}, builtIndexHTML)
	assertContains(t, html,
		`window.__VANTIGO_APP__={"basePath":"/","title":"Vantigo","logoUrl":null,"support":{"email":null,"phone":null,"url":null},"modules":[]};`)
}

func TestNewIndex_InjectsTheEnabledModules(t *testing.T) {
	idx, err := NewIndex(fstest.MapFS{"index.html": {Data: []byte(builtIndexHTML)}}, "", config.Branding{}, []string{"customers", "energy"})
	if err != nil {
		t.Fatalf("NewIndex: %v", err)
	}
	assertContains(t, string(idx.HTML), `"modules":["customers","energy"]`)
}
```

Also update `TestNewIndex_MissingIndexIsAnError` to call `NewIndex(fstest.MapFS{}, "", config.Branding{}, nil)`.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `(cd apps/server && go test ./internal/web/)`
Expected: compile error, "too many arguments in call to NewIndex".

- [ ] **Step 3: Implement**

In `apps/server/internal/web/index.go`:

```go
// NewIndex reads index.html from assets and templates it: asset URLs are
// rewritten under basePath, window.__VANTIGO_APP__ (branding plus the enabled
// module names) is injected as the first element of <head>, and <title> is
// replaced.
func NewIndex(assets fs.FS, basePath string, b config.Branding, modules []string) (*Index, error) {
	raw, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		return nil, fmt.Errorf("web: read index.html: %w", err)
	}
	script := runtimeConfigScript(basePath, b, modules)
	sum := sha256.Sum256([]byte(script))
	return &Index{
		HTML:             []byte(render(string(raw), basePath, title(b), script)),
		InlineScriptHash: "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'",
	}, nil
}
```

```go
// runtimeConfig is the shape the frontends read from window.__VANTIGO_APP__
// (see packages/frontend-shell/src/app-config.ts). Unset values are null;
// Modules is always an array so the SPA can tell "none enabled" ([]) from
// "not injected" (absent, as under the Vite dev server).
type runtimeConfig struct {
	BasePath string         `json:"basePath"`
	Title    string         `json:"title"`
	LogoURL  *string        `json:"logoUrl"`
	Support  runtimeSupport `json:"support"`
	Modules  []string       `json:"modules"`
}
```

```go
func runtimeConfigScript(basePath string, b config.Branding, modules []string) string {
	data, err := json.Marshal(runtimeConfig{
		BasePath: basePath + "/",
		Title:    title(b),
		LogoURL:  optional(b.LogoURL),
		Support: runtimeSupport{
			Email: optional(b.SupportEmail),
			Phone: optional(b.SupportPhone),
			URL:   optional(b.SupportURL),
		},
		Modules: append([]string{}, modules...),
	})
	if err != nil {
		panic("web: marshal runtime config: " + err.Error()) // impossible for this type
	}
	return "window.__VANTIGO_APP__=" + string(data) + ";"
}
```

Update both callers to pass the resolved list:

`apps/server/cmd/vantigo/main.go:369` → `index, err := web.NewIndex(assets, cfg.BasePath, cfg.Branding, cfg.Modules)`

`apps/server/internal/modtest/modtest.go:326` → `index, err := web.NewIndex(assets, cfg.BasePath, cfg.Branding, cfg.Modules)`

- [ ] **Step 4: Run the tests to verify they pass**

Run: `(cd apps/server && gofmt -l . && go build ./... && go test ./internal/web/ ./internal/modtest/...)`
Expected: no gofmt output, PASS.

- [ ] **Step 5: Commit**

```bash
git add apps/server
git commit -m "feat(web): inject the enabled module list into the SPA runtime config"
```

---

### Task 2: The frontend reads module enablement from the injected config

**Files:**
- Modify: `packages/frontend-shell/src/app-config.ts`
- Create: `packages/frontend-shell/src/app-config.test.ts`
- Create: `apps/host/frontend/src/lib/enabled-modules.ts`
- Create: `apps/host/frontend/src/lib/enabled-modules.test.ts`
- Modify: `apps/host/frontend/src/routes/__root.tsx:108-112`, `apps/host/frontend/src/routes/dashboard.tsx:247-251`, `apps/host/frontend/src/routes/customers/-customer-detail-layout.tsx:75-78`

**Interfaces:**
- Produces: `AppConfig.modules?: readonly string[]` (undefined when not injected); `enabledModuleKeys(): readonly ModuleKey[]` in `apps/host/frontend/src/lib/enabled-modules.ts`.

- [ ] **Step 1: Write the failing shell test**

`packages/frontend-shell/src/app-config.test.ts`:

```ts
// @vitest-environment jsdom
import { afterEach, describe, expect, it } from "vitest";
import { appConfig } from "./app-config";

afterEach(() => {
  delete window.__VANTIGO_APP__;
});

describe("appConfig modules", () => {
  it("is undefined when the backend injected nothing (Vite dev server)", () => {
    expect(appConfig().modules).toBeUndefined();
  });

  it("is undefined when the injected value is null", () => {
    window.__VANTIGO_APP__ = { basePath: "/", title: "Vantigo", support: {}, modules: null };
    expect(appConfig().modules).toBeUndefined();
  });

  it("passes the injected list through, including an empty one", () => {
    window.__VANTIGO_APP__ = { basePath: "/", title: "Vantigo", support: {}, modules: ["customers", "energy"] };
    expect(appConfig().modules).toEqual(["customers", "energy"]);

    window.__VANTIGO_APP__ = { basePath: "/", title: "Vantigo", support: {}, modules: [] };
    expect(appConfig().modules).toEqual([]);
  });
});
```

- [ ] **Step 2: Run it to verify it fails**

Run: `bun run --cwd packages/frontend-shell test src/app-config.test.ts`
Expected: FAIL, type/`toEqual` mismatch on `modules` (undefined instead of the list).

- [ ] **Step 3: Implement the shell side**

In `packages/frontend-shell/src/app-config.ts`:

```ts
export interface AppConfig {
  /** The base path the host is served under, with a trailing slash (e.g. "/vantigo/"). */
  basePath: string;
  /** The application title (App__Title, defaults to the app name). */
  title: string;
  /** Custom logo URL (App__LogoUrl); undefined means the bundled Vantigo logo. */
  logoUrl?: string;
  support: AppSupport;
  /**
   * The module names the backend enabled (its MODULES allowlist). Undefined
   * when nothing was injected — the Vite dev server serves an untemplated
   * index.html — which callers treat as "every module". An injected empty
   * list means none.
   */
  modules?: readonly string[];
}

interface InjectedAppConfig {
  basePath?: string;
  title?: string;
  logoUrl?: string | null;
  support?: { email?: string | null; phone?: string | null; url?: string | null };
  modules?: string[] | null;
}
```

and in `appConfig()` add `modules: injected?.modules ?? undefined,` after `support`.

- [ ] **Step 4: Write the failing host test**

`apps/host/frontend/src/lib/enabled-modules.test.ts`:

```ts
import { afterEach, describe, expect, it } from "vitest";
import { moduleKeys } from "../navigation";
import { enabledModuleKeys } from "./enabled-modules";

const inject = (modules: string[] | null | undefined) => {
  window.__VANTIGO_APP__ = { basePath: "/", title: "Vantigo", support: {}, modules };
};

afterEach(() => {
  delete window.__VANTIGO_APP__;
});

describe("enabledModuleKeys", () => {
  it("treats a missing injection as every known module (dev server)", () => {
    expect(enabledModuleKeys()).toEqual(moduleKeys);
    inject(null);
    expect(enabledModuleKeys()).toEqual(moduleKeys);
  });

  it("keeps only the injected modules, in catalog order", () => {
    inject(["energy", "customers"]);
    expect(enabledModuleKeys()).toEqual(["customers", "energy"]);
  });

  it("ignores names the frontend does not know", () => {
    inject(["identity", "billing", "products"]);
    expect(enabledModuleKeys()).toEqual(["products"]);
  });

  it("treats an injected empty list as nothing enabled", () => {
    inject([]);
    expect(enabledModuleKeys()).toEqual([]);
  });
});
```

- [ ] **Step 5: Run it to verify it fails**

Run: `bun run --cwd apps/host/frontend test src/lib/enabled-modules.test.ts`
Expected: FAIL, cannot resolve `./enabled-modules`.

- [ ] **Step 6: Implement the host helper**

`apps/host/frontend/src/lib/enabled-modules.ts`:

```ts
import { appConfig } from "@vantigo/frontend-shell";
import { type ModuleKey, moduleKeys } from "../navigation";

/**
 * The enabled module keys: the list the backend injected into the page
 * (its resolved MODULES allowlist) intersected with the modules this build
 * knows, or every known module when nothing was injected, which is the Vite
 * dev server case. Reading is synchronous; the value never changes after load.
 */
export const enabledModuleKeys = (): readonly ModuleKey[] => {
  const injected = appConfig().modules;
  if (injected === undefined) return moduleKeys;
  return moduleKeys.filter((key) => injected.includes(key));
};
```

- [ ] **Step 7: Replace the three hardcoded constants**

`apps/host/frontend/src/routes/__root.tsx`: replace lines 108–112 (the comment and `const enabledModules = moduleKeys;`) with

```ts
  const enabledModules = enabledModuleKeys();
```

add `import { enabledModuleKeys } from "../lib/enabled-modules";` and drop `moduleKeys` from the `../navigation` import.

`apps/host/frontend/src/routes/dashboard.tsx`: replace lines 247–251 (comment and `const enabledModules: readonly ModuleKey[] = moduleKeys;`) with

```ts
  const enabledModules = enabledModuleKeys();
```

add `import { enabledModuleKeys } from "../lib/enabled-modules";` and change the navigation import to `import { hasPermissions, type ModuleKey } from "../navigation";`.

`apps/host/frontend/src/routes/customers/-customer-detail-layout.tsx`: replace lines 75–78 (comment and the `visibleCustomerDetailTabs(moduleKeys, …)` call) with

```ts
  const visibleTabs = visibleCustomerDetailTabs(enabledModuleKeys(), authorization.data?.permissions);
```

add `import { enabledModuleKeys } from "../../lib/enabled-modules";` and change the navigation import to `import { hasPermissions, type ModuleKey } from "../../navigation";`.

- [ ] **Step 8: Run the tests and type-check**

Run: `bun run --cwd packages/frontend-shell test && bun run --cwd apps/host/frontend test && bun run frontend:typecheck`
Expected: all PASS (the existing `customer-detail-route.test.tsx` still sees every module because nothing is injected under vitest).

- [ ] **Step 9: Commit**

```bash
git add packages/frontend-shell/src/app-config.ts packages/frontend-shell/src/app-config.test.ts apps/host/frontend/src/lib apps/host/frontend/src/routes/__root.tsx apps/host/frontend/src/routes/dashboard.tsx apps/host/frontend/src/routes/customers/-customer-detail-layout.tsx
git commit -m "feat(frontend): read module enablement from the injected runtime config"
```

---

### Task 3: Move contacts and inbox under their app prefixes

**Files:**
- Move: `apps/host/frontend/src/routes/contacts/index.tsx` → `apps/host/frontend/src/routes/customers/contacts/index.tsx`
- Move: `apps/host/frontend/src/routes/contacts/$contactId.tsx` → `apps/host/frontend/src/routes/customers/contacts/$contactId.tsx`
- Move: `apps/host/frontend/src/routes/inbox.tsx` → `apps/host/frontend/src/routes/communications/inbox.tsx`
- Create: `apps/host/frontend/src/routes/communications/index.tsx`, `apps/host/frontend/src/routes/energy/index.tsx`
- Modify: `apps/host/frontend/src/routes/route-guards.test.ts`
- Modify (links): `apps/host/frontend/src/navigation.ts:66,79`, `apps/host/frontend/src/navigation.test.ts:81-82`, `apps/host/frontend/src/routes/dashboard.tsx:118,414,419,432,762`, `apps/host/frontend/src/routes/customers/-customer-detail-layout.tsx:92`, `apps/host/frontend/src/components/app-spotlight.tsx:74,184`, `apps/customers/frontend/src/pages/contacts.$contactId.tsx:54`, `apps/customers/frontend/src/pages/contacts.index.tsx:145`, `apps/customers/frontend/src/pages/-customer-contacts-card.tsx:139`, `apps/customers/frontend/src/components/app-spotlight.tsx:20,104`, `apps/customers/frontend/src/test/route-tree.tsx:140-146`
- Regenerated: `apps/host/frontend/src/routeTree.gen.ts`

**Interfaces:**
- Produces routes `/customers/contacts`, `/customers/contacts/$contactId`, `/communications/inbox`, and redirects `/communications` → `/communications/inbox`, `/energy` → `/energy/metering-points`.

- [ ] **Step 1: Write the failing redirect tests**

Append to `apps/host/frontend/src/routes/route-guards.test.ts` (add the two imports next to the existing route imports):

```ts
import { Route as CommunicationsIndexRoute } from "./communications/index";
import { Route as EnergyIndexRoute } from "./energy/index";
```

```ts
describe("app index routes", () => {
  it("sends /communications to the inbox", async () => {
    fetchSession.mockResolvedValue(sessionWithRoles(["Member"]));

    expect(CommunicationsIndexRoute.options.component).toBeUndefined();
    await expectRedirectTo(CommunicationsIndexRoute, "/communications/inbox");
  });

  it("sends /energy to the metering points", async () => {
    fetchSession.mockResolvedValue(sessionWithRoles(["Member"]));

    expect(EnergyIndexRoute.options.component).toBeUndefined();
    await expectRedirectTo(EnergyIndexRoute, "/energy/metering-points");
  });
});
```

- [ ] **Step 2: Run it to verify it fails**

Run: `bun run --cwd apps/host/frontend test src/routes/route-guards.test.ts`
Expected: FAIL, cannot resolve `./communications/index`.

- [ ] **Step 3: Move the route files and fix their route ids**

```bash
cd apps/host/frontend/src/routes
git mv contacts/index.tsx customers/contacts/index.tsx
git mv 'contacts/$contactId.tsx' 'customers/contacts/$contactId.tsx'
git mv inbox.tsx communications/inbox.tsx
cd -
```

Edit the `createFileRoute` ids: `"/contacts/"` → `"/customers/contacts/"`, `"/contacts/$contactId"` → `"/customers/contacts/$contactId"`, `"/inbox"` → `"/communications/inbox"`. (The generator would rewrite these on its own, but editing them keeps the diff honest.)

- [ ] **Step 4: Add the two redirect routes**

`apps/host/frontend/src/routes/communications/index.tsx`:

```tsx
import { createFileRoute, redirect } from "@tanstack/react-router";

// The communications app prefix is a pure redirect: the inbox is its home.
export const Route = createFileRoute("/communications/")({
  beforeLoad: () => {
    // The inbox validates its search params, so TanStack requires every key
    // on a typed navigation; these are what its validator derives from an
    // empty query string.
    throw redirect({
      to: "/communications/inbox",
      search: { conversationId: undefined, status: undefined, customerId: undefined, tagId: undefined, unreadOnly: undefined },
    });
  },
});
```

`apps/host/frontend/src/routes/energy/index.tsx`:

```tsx
import { createFileRoute, redirect } from "@tanstack/react-router";

// The energy app prefix is a pure redirect: metering points are its home.
export const Route = createFileRoute("/energy/")({
  beforeLoad: () => {
    throw redirect({ to: "/energy/metering-points", search: { page: 1, search: "" } });
  },
});
```

- [ ] **Step 5: Regenerate the route tree**

Run: `(cd apps/host/frontend && bunx vite build) && git status --short apps/host/frontend/src/routeTree.gen.ts`
Expected: the generated file shows as modified; it now imports `./routes/communications/inbox`, `./routes/communications/index`, `./routes/energy/index`, `./routes/customers/contacts/index`, `./routes/customers/contacts/$contactId` and no `./routes/inbox` or `./routes/contacts/*`.

- [ ] **Step 6: Update every link to the moved paths**

Host (typed links become type errors after step 5; untyped ones must be edited by hand):

- `apps/host/frontend/src/navigation.ts`: line 66 `to: "/contacts"` → `to: "/customers/contacts"`; line 79 `to: "/inbox"` → `to: "/communications/inbox"`.
- `apps/host/frontend/src/navigation.test.ts` lines 81–82: `"/contacts"` → `"/customers/contacts"`, `"/inbox"` → `"/communications/inbox"`.
- `apps/host/frontend/src/routes/dashboard.tsx`: line 118 `path: "/inbox"` → `path: "/communications/inbox"`; line 414 `` `/inbox?conversationId=…` `` → `` `/communications/inbox?conversationId=${encodeURIComponent(item.entityId)}` ``; line 419 `return "/inbox"` → `return "/communications/inbox"`; line 432 `href: "/inbox"` → `href: "/communications/inbox"`; line 762 `to="/inbox"` → `to="/communications/inbox"`.
- `apps/host/frontend/src/routes/customers/-customer-detail-layout.tsx:92`: `{ to: "/inbox", search: … }` → `{ to: "/communications/inbox", search: … }`.
- `apps/host/frontend/src/components/app-spotlight.tsx`: line 74 `path: "/inbox"` → `path: "/communications/inbox"`; line 184 `to: "/contacts/$contactId"` → `to: "/customers/contacts/$contactId"`.

Customers package (all untyped, grep-verified):

- `apps/customers/frontend/src/pages/contacts.$contactId.tsx:54`: `to={"/contacts" as never}` → `to={"/customers/contacts" as never}`.
- `apps/customers/frontend/src/pages/contacts.index.tsx:145`: `` href: `/contacts/${item.contact.id}` `` → `` href: `/customers/contacts/${item.contact.id}` ``.
- `apps/customers/frontend/src/pages/-customer-contacts-card.tsx:139`: `` href: `/contacts/${association.contact.id}` `` → `` href: `/customers/contacts/${association.contact.id}` ``.
- `apps/customers/frontend/src/components/app-spotlight.tsx`: line 20 `to: "/contacts"` → `to: "/customers/contacts"`; line 104 `to: "/contacts/$contactId"` → `to: "/customers/contacts/$contactId"`.
- `apps/customers/frontend/src/test/route-tree.tsx:140-146`: `route("/contacts", …)` → `route("/customers/contacts", …)`, `route("/contacts/$contactId", …)` → `route("/customers/contacts/$contactId", …)`, and the `<Button component={Link} to="/contacts">` inside it → `to="/customers/contacts"`.

- [ ] **Step 7: Verify nothing references the old paths**

Run: `grep -rn --include='*.ts' --include='*.tsx' -E '"/(contacts|inbox)(/|"|\$)|`/(contacts|inbox)' apps/*/frontend/src packages/*/src | grep -v routeTree.gen`
Expected: no output. (API URLs like `/api/v1/customers/contacts` do not match this pattern.)

- [ ] **Step 8: Run tests, type-check, lint**

Run: `bun run --cwd apps/host/frontend test && bun run --cwd apps/customers/frontend test && bun run frontend:typecheck && bun run frontend:lint`
Expected: all PASS.

- [ ] **Step 9: Commit**

```bash
git add -A apps/host/frontend/src apps/customers/frontend/src
git commit -m "refactor(frontend): move contacts and inbox under their app prefixes"
```

---

### Task 4: The app registry and the account-menu catalog

**Files:**
- Create: `apps/host/frontend/src/apps.ts`, `apps/host/frontend/src/apps.test.ts`
- Create: `apps/host/frontend/src/account-menu.ts`, `apps/host/frontend/src/account-menu.test.ts`
- Modify: `apps/host/frontend/src/navigation.ts` (rewrite), `apps/host/frontend/src/navigation.test.ts` (rewrite)
- Modify: `apps/host/frontend/src/catalogs/navigation.ts`
- Modify: `apps/host/frontend/src/components/module-access-guard.tsx:7,18`, `apps/host/frontend/src/components/app-spotlight.tsx`, `apps/host/frontend/src/components/app-spotlight.test.tsx`, `apps/host/frontend/src/routes/__root.tsx`

**Interfaces:**
- Consumes: `ModuleKey`, `NavItem`, `NavSection`, `hasPermissions` from `navigation.ts`.
- Produces (in `navigation.ts`): `visibleNavSections<S extends NavSection>(sections: readonly S[], context: NavVisibilityContext): S[]`; `NavSection` loses `placement`; the global `navSections` export is removed.
- Produces (in `apps.ts`): `type AppKey = "home" | ModuleKey`; `interface AppDefinition { key; module?; label; icon; home: string; navSections; requiredPermissions? }`; `apps: readonly AppDefinition[]`; `allNavSections: readonly NavSection[]`; `appForKey(key: AppKey): AppDefinition`; `isAppEnabled(app, enabledModules): boolean`; `activeAppKey(matches): AppKey | undefined`; `switcherTiles(permissions, enabledModules, activeKey): SwitcherTile[]` with `SwitcherTile = { app: AppDefinition; current: boolean; enabled: boolean }`; and the TanStack `StaticDataRouteOption.app?: AppKey` augmentation.
- Produces (in `account-menu.ts`): `interface AccountMenuSection { label: string; items: readonly NavItem[] }`; `accountMenuSections: readonly AccountMenuSection[]`.
- Produces (in `app-spotlight.tsx`): `spotlightNavSections: readonly NavSection[]` (dashboard + every app + account menu), used by its test.

- [ ] **Step 1: Write the failing tests**

Replace `apps/host/frontend/src/navigation.test.ts` entirely:

```ts
import { IconCircle } from "@tabler/icons-react";
import { describe, expect, it } from "vitest";
import { activeNavPath, hasPermissions, type NavSection, navSearchFor, visibleNavSections } from "./navigation";

const icon = IconCircle;
const fixture: readonly NavSection[] = [
  {
    items: [
      { label: "open", to: "/open", icon },
      { label: "customers", to: "/customers", icon, module: "customers", requiredPermissions: ["customers:view"] },
      { label: "products", to: "/products", icon, module: "products", requiredPermissions: ["products:products-view"] },
      { label: "categories", to: "/products/categories", icon, module: "products" },
      { label: "channels", to: "/communications/channels", icon, module: "communications" },
    ],
  },
  {
    label: "admin",
    items: [
      { label: "owner", to: "/owner", icon, ownerOnly: true },
      { label: "roles", to: "/roles", icon, capability: "authorization" },
      { label: "system", to: "/system", icon, systemAdminOnly: true },
    ],
  },
];
const allModules = ["communications", "customers", "energy", "products"] as const;
const context = (overrides: Partial<Parameters<typeof visibleNavSections>[1]> = {}) => ({
  permissions: ["*"],
  isOwner: false,
  canManageAuthorization: false,
  enabledModules: allModules,
  ...overrides,
});
const labels = (sections: readonly NavSection[]) => sections.flatMap((section) => section.items.map((item) => item.label));

describe("navigation permissions", () => {
  it("grants access when any declared permission is held", () => {
    expect(hasPermissions(undefined)).toBe(true);
    expect(hasPermissions([], ["customers:view"])).toBe(false);
    expect(hasPermissions(["customers:view"], ["customers:view"])).toBe(true);
    expect(hasPermissions(["customers:view"], ["customers:view", "customers:contacts-view"])).toBe(true);
    expect(hasPermissions(["products:products-view"], ["customers:view", "customers:contacts-view"])).toBe(false);
    expect(hasPermissions(["*"], ["customers:view", "customers:contacts-view"])).toBe(true);
  });

  it("filters by permission and drops nothing else for a plain member", () => {
    expect(labels(visibleNavSections(fixture, context({ permissions: ["customers:view"] })))).toEqual([
      "open",
      "customers",
      "categories",
      "channels",
    ]);
  });

  it("hides destinations of disabled modules", () => {
    expect(labels(visibleNavSections(fixture, context({ enabledModules: ["customers"] })))).toEqual(["open", "customers"]);
  });

  it("hides module destinations while enabled modules are unknown", () => {
    expect(labels(visibleNavSections(fixture, context({ enabledModules: undefined })))).toEqual(["open"]);
  });

  it("gates owner, authorization and system admin items separately", () => {
    expect(labels(visibleNavSections(fixture, context({ isOwner: true })))).toContain("owner");
    expect(labels(visibleNavSections(fixture, context({ isOwner: true })))).not.toContain("roles");
    expect(labels(visibleNavSections(fixture, context({ canManageAuthorization: true })))).toContain("roles");
    expect(labels(visibleNavSections(fixture, context({ isOwner: true, canManageAuthorization: true })))).not.toContain(
      "system",
    );
    expect(labels(visibleNavSections(fixture, context({ isSystemAdmin: true })))).toContain("system");
  });

  it("drops sections that end up empty", () => {
    const sections = visibleNavSections(fixture, context());
    expect(sections.map((section) => section.label)).toEqual([undefined]);
  });

  it("preserves extra fields on the section type it is given", () => {
    const typed = [{ label: "admin", extra: 1, items: fixture[1]!.items }];
    const [section] = visibleNavSections(typed, context({ isOwner: true }));
    expect(section?.extra).toBe(1);
  });

  it("provides search defaults only for list destinations", () => {
    expect(navSearchFor("customer-list")).toEqual({ page: 1, search: "" });
    expect(navSearchFor("inbox-list")).toEqual({
      conversationId: undefined,
      status: undefined,
      customerId: undefined,
      tagId: undefined,
      unreadOnly: undefined,
    });
    expect(navSearchFor("products-list")).toEqual({ page: 1, search: "", status: "", categoryId: "" });
    expect(navSearchFor(undefined)).toBeUndefined();
  });
});

describe("active navigation paths", () => {
  const items = fixture.flatMap((section) => section.items);

  it("chooses the longest matching prefix for nested routes", () => {
    expect(activeNavPath("/products/categories/42", items)).toBe("/products/categories");
    expect(activeNavPath("/communications/channels/new", items)).toBe("/communications/channels");
  });

  it("does not treat a similarly prefixed route as a match", () => {
    expect(activeNavPath("/products-archive", items)).toBeUndefined();
    expect(activeNavPath("/customerships", items)).toBeUndefined();
  });
});
```

Create `apps/host/frontend/src/apps.test.ts`:

```ts
import { describe, expect, it } from "vitest";
import { activeAppKey, allNavSections, appForKey, apps, isAppEnabled, switcherTiles } from "./apps";

const allModules = ["communications", "customers", "energy", "products"] as const;

describe("the app registry", () => {
  it("lists Home first, without a module, and every module app once", () => {
    expect(apps[0]).toMatchObject({ key: "home", home: "/dashboard", navSections: [] });
    expect(apps[0]?.module).toBeUndefined();
    expect(apps.map((app) => app.key)).toEqual(["home", "customers", "communications", "products", "energy"]);
    for (const app of apps.slice(1)) expect(app.module).toBe(app.key);
  });

  it("keeps every sidebar destination under its app's URL prefix", () => {
    for (const app of apps) {
      const prefix = `/${app.key}`;
      for (const item of app.navSections.flatMap((section) => section.items)) {
        expect(item.to === prefix || item.to.startsWith(`${prefix}/`)).toBe(true);
        expect(item.module).toBe(app.module);
      }
    }
  });

  it("addresses every destination by a bare path", () => {
    const paths = allNavSections.flatMap((section) => section.items.map((item) => item.to));
    for (const path of paths) expect(path).toMatch(/^\/[a-z-]+(\/[a-z-]+)*$/);
    expect(paths).toEqual([
      "/customers",
      "/customers/contacts",
      "/communications/inbox",
      "/communications/channels",
      "/communications/suppressions",
      "/products",
      "/products/categories",
      "/energy/metering-points",
    ]);
  });

  it("requires, for a tile, any permission that unlocks one of its sidebar entries", () => {
    expect(appForKey("customers").requiredPermissions).toEqual([
      "customers:view",
      "customers:contacts-view",
      "customers:associations-view",
    ]);
    expect(appForKey("home").requiredPermissions).toBeUndefined();
  });

  it("throws for an unknown key instead of returning undefined", () => {
    expect(() => appForKey("billing" as never)).toThrow(/billing/);
  });

  it("treats Home as always enabled and module apps as enabled when their module is", () => {
    expect(isAppEnabled(appForKey("home"), [])).toBe(true);
    expect(isAppEnabled(appForKey("energy"), ["customers"])).toBe(false);
    expect(isAppEnabled(appForKey("energy"), ["customers", "energy"])).toBe(true);
  });
});

describe("activeAppKey", () => {
  it("takes the deepest match that carries an app", () => {
    expect(activeAppKey([{ staticData: {} }, { staticData: { app: "customers" } }, { staticData: {} }])).toBe("customers");
  });

  it("is undefined when no match carries an app (administration and public paths)", () => {
    expect(activeAppKey([{ staticData: {} }, { staticData: {} }])).toBeUndefined();
    expect(activeAppKey([])).toBeUndefined();
  });
});

describe("switcherTiles", () => {
  it("hides apps the user has no permission for, keeps Home, marks current and disabled", () => {
    const tiles = switcherTiles(["customers:view"], ["customers", "products"], "customers");
    expect(tiles.map((tile) => tile.app.key)).toEqual(["home", "customers"]);
    expect(tiles.map((tile) => tile.current)).toEqual([false, true]);
    expect(tiles.map((tile) => tile.enabled)).toEqual([true, true]);
  });

  it("shows a disabled module the user could otherwise use, muted rather than hidden", () => {
    const tiles = switcherTiles(["*"], ["customers"], undefined);
    expect(tiles.map((tile) => [tile.app.key, tile.enabled])).toEqual([
      ["home", true],
      ["customers", true],
      ["communications", false],
      ["products", false],
      ["energy", false],
    ]);
    expect(tiles.some((tile) => tile.current)).toBe(false);
  });

  it("shows only Home while permissions are still loading", () => {
    expect(switcherTiles(undefined, allModules, "home").map((tile) => tile.app.key)).toEqual(["home"]);
  });
});
```

Create `apps/host/frontend/src/account-menu.test.ts`:

```ts
import { describe, expect, it } from "vitest";
import { accountMenuSections } from "./account-menu";
import { visibleNavSections } from "./navigation";

const context = (overrides: Partial<Parameters<typeof visibleNavSections>[1]> = {}) => ({
  permissions: ["*"],
  isOwner: false,
  canManageAuthorization: false,
  enabledModules: [],
  ...overrides,
});
const visible = (overrides: Parameters<typeof context>[0] = {}) =>
  visibleNavSections(accountMenuSections, context(overrides)).map((section) => ({
    label: section.label,
    items: section.items.map((item) => item.to),
  }));

describe("account menu catalog", () => {
  it("shows a plain member only their own account section", () => {
    expect(visible()).toEqual([{ label: "navigation.accountSection", items: ["/settings"] }]);
  });

  it("adds the workspace section for owners and authorization managers", () => {
    expect(visible({ isOwner: true })).toEqual([
      { label: "navigation.accountSection", items: ["/settings"] },
      { label: "navigation.workspaceSection", items: ["/workspace/overview"] },
    ]);
    expect(visible({ canManageAuthorization: true })[1]).toEqual({
      label: "navigation.workspaceSection",
      items: ["/workspace/roles"],
    });
    expect(visible({ isOwner: true, canManageAuthorization: true })[1]?.items).toEqual([
      "/workspace/overview",
      "/workspace/roles",
    ]);
  });

  it("adds the system section only for system admins", () => {
    expect(visible({ isOwner: true, canManageAuthorization: true }).map((section) => section.label)).not.toContain(
      "navigation.systemSection",
    );
    expect(visible({ isSystemAdmin: true }).at(-1)).toEqual({ label: "navigation.systemSection", items: ["/admin"] });
  });

  it("keeps workspace administration on its own segment and off the users/invitations tabs", () => {
    const items = accountMenuSections.flatMap((section) => section.items);
    for (const item of items.filter((item) => item.ownerOnly || item.capability)) {
      expect(item.to.startsWith("/workspace/")).toBe(true);
    }
    expect(items.map((item) => item.to)).not.toContain("/workspace/users");
    expect(items.map((item) => item.to)).not.toContain("/workspace/invitations");
  });
});
```

- [ ] **Step 2: Run them to verify they fail**

Run: `bun run --cwd apps/host/frontend test src/navigation.test.ts src/apps.test.ts src/account-menu.test.ts`
Expected: FAIL (missing modules, and `visibleNavSections` called with two arguments).

- [ ] **Step 3: Rewrite `navigation.ts`**

Replace the whole file with:

```ts
import type { ComponentType } from "react";

// The module keys this build knows. Which of them are enabled comes from the
// injected runtime config (see lib/enabled-modules.ts); which destinations
// belong to which module is declared by the app registry (apps.ts).
export const moduleKeys = ["communications", "customers", "energy", "products"] as const;
export type ModuleKey = (typeof moduleKeys)[number];

export interface NavItem {
  label: string;
  to: string;
  icon: ComponentType<{ size?: number | string; stroke?: number | string }>;
  ownerOnly?: boolean;
  systemAdminOnly?: boolean;
  capability?: "authorization";
  requiredPermissions?: readonly string[];
  /** The module that must be enabled for this destination. */
  module?: ModuleKey;
  /** Search defaults used when Spotlight opens this destination. */
  searchStrategy?: "customer-list" | "inbox-list" | "products-list" | "energy-list";
}
export interface NavSection {
  /** Optional section heading; unlabeled sections render items only. */
  label?: string;
  items: readonly NavItem[];
}

export const hasPermissions = (permissions: string[] | undefined, required?: readonly string[]) =>
  !required?.length ||
  permissions?.includes("*") === true ||
  required.some((permission) => permissions?.includes(permission));

export interface NavVisibilityContext {
  permissions: string[] | undefined;
  isOwner: boolean;
  canManageAuthorization: boolean;
  isSystemAdmin?: boolean;
  /** Enabled module keys; undefined while unknown (hides module destinations). */
  enabledModules?: readonly ModuleKey[];
}

/**
 * Filters sections to the items the caller may see and drops sections that
 * end up empty. Generic over the section type so catalogs with extra fields
 * (the account menu's required label) keep them.
 */
export const visibleNavSections = <S extends NavSection>(
  sections: readonly S[],
  { permissions, isOwner, canManageAuthorization, isSystemAdmin = false, enabledModules }: NavVisibilityContext,
): S[] =>
  sections
    .map((section) => ({
      ...section,
      items: section.items.filter(
        (item) =>
          (!item.ownerOnly || isOwner) &&
          (!item.systemAdminOnly || isSystemAdmin) &&
          (!item.capability || canManageAuthorization) &&
          (!item.module || enabledModules?.includes(item.module) === true) &&
          hasPermissions(permissions, item.requiredPermissions),
      ),
    }))
    .filter((section) => section.items.length > 0);

export const navSearchFor = (strategy: NavItem["searchStrategy"]) => {
  switch (strategy) {
    case "customer-list":
      return { page: 1, search: "" };
    case "inbox-list":
      return {
        conversationId: undefined,
        status: undefined,
        customerId: undefined,
        tagId: undefined,
        unreadOnly: undefined,
      };
    case "products-list":
      return { page: 1, search: "", status: "", categoryId: "" };
    case "energy-list":
      return { page: 1, search: "" };
    default:
      return undefined;
  }
};

// No nav destination is "/" (the root route is a pure redirect, never a nav
// target), so matching only needs the prefix form.
export const activeNavPath = (pathname: string, items: readonly NavItem[]) => {
  let best: string | undefined;
  for (const item of items) {
    const matches = pathname === item.to || pathname.startsWith(`${item.to}/`);
    if (matches && (best === undefined || item.to.length > best.length)) best = item.to;
  }
  return best;
};
```

- [ ] **Step 4: Create `apps.ts`**

```ts
import {
  IconAddressBook,
  IconBolt,
  IconCategory,
  IconInbox,
  IconLayoutDashboard,
  IconMailbox,
  IconMailOff,
  IconPackage,
  IconUsers,
} from "@tabler/icons-react";
import { hasPermissions, type ModuleKey, type NavItem, type NavSection } from "./navigation";

export type AppKey = "home" | ModuleKey;

declare module "@tanstack/react-router" {
  interface StaticDataRouteOption {
    /** The app a route subtree belongs to. Set only on app layout routes and the dashboard. */
    app?: AppKey;
  }
}

export interface AppDefinition {
  key: AppKey;
  /** The backend module that must be enabled; undefined for Home. */
  module?: ModuleKey;
  /** i18n key in the host navigation catalog. */
  label: string;
  icon: NavItem["icon"];
  /** Where the switcher tile navigates. Untyped like the sidebar links; /dashboard derives its own search defaults. */
  home: string;
  /** Sidebar sections; empty means the app renders without a sidebar. */
  navSections: readonly NavSection[];
  /** The tile is hidden unless the user holds one of these. Undefined = always shown. */
  requiredPermissions?: readonly string[];
}

const permissionsUnlocking = (items: readonly NavItem[]) => [
  ...new Set(items.flatMap((item) => item.requiredPermissions ?? [])),
];

/** A module app: one unlabeled sidebar section, a tile shown when any of its entries would be. */
const moduleApp = (
  key: ModuleKey,
  label: string,
  icon: NavItem["icon"],
  home: string,
  items: readonly Omit<NavItem, "module">[],
): AppDefinition => {
  const tagged = items.map((item) => ({ ...item, module: key }));
  return { key, module: key, label, icon, home, navSections: [{ items: tagged }], requiredPermissions: permissionsUnlocking(tagged) };
};

/** Every app, in switcher order. Home has no module and no sidebar. */
export const apps: readonly AppDefinition[] = [
  { key: "home", label: "navigation.home", icon: IconLayoutDashboard, home: "/dashboard", navSections: [] },
  moduleApp("customers", "navigation.customers", IconUsers, "/customers", [
    {
      label: "navigation.customers",
      to: "/customers",
      icon: IconUsers,
      requiredPermissions: ["customers:view"],
      searchStrategy: "customer-list",
    },
    {
      label: "navigation.contacts",
      to: "/customers/contacts",
      icon: IconAddressBook,
      requiredPermissions: ["customers:contacts-view", "customers:associations-view"],
      searchStrategy: "customer-list",
    },
  ]),
  moduleApp("communications", "navigation.communications", IconInbox, "/communications", [
    {
      label: "navigation.inbox",
      to: "/communications/inbox",
      icon: IconInbox,
      requiredPermissions: ["communications:conversations-view"],
      searchStrategy: "inbox-list",
    },
    {
      label: "navigation.channels",
      to: "/communications/channels",
      icon: IconMailbox,
      requiredPermissions: ["communications:channels-manage"],
    },
    {
      label: "navigation.suppressions",
      to: "/communications/suppressions",
      icon: IconMailOff,
      requiredPermissions: ["communications:suppressions-manage"],
    },
  ]),
  moduleApp("products", "navigation.products", IconPackage, "/products", [
    {
      label: "navigation.products",
      to: "/products",
      icon: IconPackage,
      requiredPermissions: [
        "products:products-view",
        "products:variants-view",
        "products:pricing-view",
        "products:categories-view",
        "products:tax-categories-view",
      ],
      searchStrategy: "products-list",
    },
    {
      label: "navigation.categories",
      to: "/products/categories",
      icon: IconCategory,
      requiredPermissions: ["products:categories-view"],
    },
  ]),
  moduleApp("energy", "navigation.energy", IconBolt, "/energy", [
    {
      label: "navigation.meteringPoints",
      to: "/energy/metering-points",
      icon: IconBolt,
      requiredPermissions: ["energy:metering-points-view", "energy:meters-view"],
      searchStrategy: "energy-list",
    },
  ]),
];

/** Every sidebar destination across apps, for consumers that span apps (spotlight, permission guard). */
export const allNavSections: readonly NavSection[] = apps.flatMap((app) => app.navSections);

export const appForKey = (key: AppKey): AppDefinition => {
  const app = apps.find((candidate) => candidate.key === key);
  if (!app) throw new Error(`unknown app "${key}"`);
  return app;
};

export const isAppEnabled = (app: AppDefinition, enabledModules: readonly ModuleKey[]) =>
  app.module === undefined || enabledModules.includes(app.module);

/** The app of the deepest matched route that declares one; undefined on administration and public paths. */
export const activeAppKey = (matches: ReadonlyArray<{ staticData?: { app?: AppKey } }>): AppKey | undefined => {
  for (let index = matches.length - 1; index >= 0; index -= 1) {
    const key = matches[index]?.staticData?.app;
    if (key) return key;
  }
  return undefined;
};

export interface SwitcherTile {
  app: AppDefinition;
  current: boolean;
  /** False when the app's module is turned off for this installation. */
  enabled: boolean;
}

/**
 * The switcher's tiles: apps the user may use (permission is a user
 * property, so the rest are absent), each marked current and enabled
 * (enablement is an installation property, so disabled apps stay visible).
 */
export const switcherTiles = (
  permissions: string[] | undefined,
  enabledModules: readonly ModuleKey[],
  activeKey: AppKey | undefined,
): SwitcherTile[] =>
  apps
    .filter((app) => hasPermissions(permissions, app.requiredPermissions))
    .map((app) => ({ app, current: app.key === activeKey, enabled: isAppEnabled(app, enabledModules) }));
```

- [ ] **Step 5: Create `account-menu.ts`**

```ts
import { IconBuildingSkyscraper, IconLayoutDashboard, IconSettings, IconShieldCheck } from "@tabler/icons-react";
import type { NavItem } from "./navigation";

export interface AccountMenuSection {
  /** i18n key for the section heading. */
  label: string;
  items: readonly NavItem[];
}

/**
 * The avatar menu's destinations, grouped. Personal account settings
 * (/settings) stay distinct from workspace administration (/workspace): the
 * latter is Owner-gated, the former is open to every signed-in user. Users
 * and invitations remain in-page tabs of the workspace area.
 */
export const accountMenuSections: readonly AccountMenuSection[] = [
  {
    label: "navigation.accountSection",
    items: [{ label: "navigation.settings", to: "/settings", icon: IconSettings }],
  },
  {
    label: "navigation.workspaceSection",
    items: [
      { label: "navigation.workspaceAdmin", to: "/workspace/overview", icon: IconLayoutDashboard, ownerOnly: true },
      { label: "navigation.rolesAccess", to: "/workspace/roles", icon: IconShieldCheck, capability: "authorization" },
    ],
  },
  {
    label: "navigation.systemSection",
    items: [
      { label: "navigation.systemAdmin", to: "/admin", icon: IconBuildingSkyscraper, systemAdminOnly: true },
    ],
  },
];
```

- [ ] **Step 6: Update the navigation catalog**

In `apps/host/frontend/src/catalogs/navigation.ts`, remove the keys `navigation.customerWorkspace`, `navigation.catalog`, `navigation.settingsAdministration`, `navigation.adminDashboard` from both languages, and add to `en`:

```ts
  "navigation.home": "Home",
  "navigation.accountSection": "Your account",
  "navigation.workspaceSection": "Workspace",
  "navigation.workspaceAdmin": "Workspace admin",
  "navigation.systemSection": "System",
  "navigation.notEnabled": "Not enabled",
```

and to `nb`:

```ts
  "navigation.home": "Hjem",
  "navigation.accountSection": "Din konto",
  "navigation.workspaceSection": "Arbeidsområde",
  "navigation.workspaceAdmin": "Administrer arbeidsområde",
  "navigation.systemSection": "System",
  "navigation.notEnabled": "Ikke aktivert",
```

(Keep `navigation.communications`, `navigation.energy`, `navigation.products`, `navigation.customers`: they now double as app labels.)

- [ ] **Step 7: Update the consumers of the old global list**

`apps/host/frontend/src/components/module-access-guard.tsx`: change line 7 to `import { hasPermissions, type ModuleKey } from "../navigation";`, add `import { allNavSections } from "../apps";`, and change line 18 to `const rules: ModuleAccessRule[] = allNavSections`. Update the comment above it to "The app registry is the single source of truth …".

`apps/host/frontend/src/components/app-spotlight.tsx`:

```ts
import { IconBolt, IconBuilding, IconLayoutDashboard, IconMail, IconPackage, IconPlus, IconSearch, IconUser } from "@tabler/icons-react";
…
import { accountMenuSections } from "../account-menu";
import { allNavSections } from "../apps";
import { hasPermissions, type ModuleKey, type NavSection, navSearchFor, visibleNavSections } from "../navigation";

/**
 * Everything the spotlight can navigate to: the dashboard (Home has no
 * sidebar, so it is not in any app's sections), every app's sidebar, and the
 * avatar menu's destinations. Filtered per user at render time.
 */
export const spotlightNavSections: readonly NavSection[] = [
  { items: [{ label: "navigation.dashboard", to: "/dashboard", icon: IconLayoutDashboard }] },
  ...allNavSections,
  ...accountMenuSections,
];
```

and replace the `navigationActions` computation with:

```ts
  const navigationActions = visibleNavSections(spotlightNavSections, {
    permissions,
    isOwner,
    canManageAuthorization,
    isSystemAdmin,
    enabledModules,
  }).flatMap((section) =>
    section.items.map((item) => ({
      ...item,
      description: t("navigation.open", { label: t(item.label).toLowerCase() }),
    })),
  );
```

`apps/host/frontend/src/components/app-spotlight.test.tsx`: change the navigation import to `import { type ModuleKey, visibleNavSections } from "../navigation";`, import `spotlightNavSections` from `./app-spotlight`, and in `expectNavigationParity` use `visibleNavSections(spotlightNavSections, {...})` for `expected` and `spotlightNavSections.flatMap(...)` for `restricted`.

`apps/host/frontend/src/routes/__root.tsx` (a minimal edit, the real composition comes in Task 8): import `allNavSections` from `../apps` and `accountMenuSections` from `../account-menu`; replace the `visibleSections` / `primarySections` / `lowerSections` block with

```ts
  const visibility = {
    permissions,
    isOwner,
    canManageAuthorization,
    isSystemAdmin: session.isSystemAdmin,
    enabledModules,
  };
  const primarySections = visibleNavSections(allNavSections, visibility);
  const lowerSections = visibleNavSections(accountMenuSections, visibility);
```

- [ ] **Step 8: Run the tests, type-check, lint, translations**

Run: `bun run --cwd apps/host/frontend test && bun run frontend:typecheck && bun run frontend:lint && bun run translations:check`
Expected: all PASS. (`module-access-guard.test.tsx` and `app-spotlight.test.tsx` keep passing: the same destinations exist, only their source changed.)

- [ ] **Step 9: Commit**

```bash
git add apps/host/frontend/src
git commit -m "feat(frontend): declare apps in a registry and move settings into an account-menu catalog"
```

---

### Task 5: App layout routes with the not-enabled gate

**Files:**
- Create: `apps/host/frontend/src/components/errors/module-not-enabled.tsx`; modify `apps/host/frontend/src/components/errors/index.ts`
- Modify: `apps/host/frontend/src/catalogs/error.ts`
- Create: `apps/host/frontend/src/routes/-app-layout.tsx`, `apps/host/frontend/src/routes/-app-layout.test.tsx`
- Create: `apps/host/frontend/src/routes/customers.tsx`, `apps/host/frontend/src/routes/communications.tsx`, `apps/host/frontend/src/routes/products.tsx`, `apps/host/frontend/src/routes/energy.tsx`
- Modify: `apps/host/frontend/src/routes/dashboard.tsx:897`, `apps/host/frontend/src/routes/customers/$customerId.energy.tsx`
- Regenerated: `apps/host/frontend/src/routeTree.gen.ts`

**Interfaces:**
- Consumes: `appForKey`, `isAppEnabled`, `AppKey` from `apps.ts`; `enabledModuleKeys()`; `ErrorPage`, `ForbiddenIllustration` from `components/errors`.
- Produces: `ModuleNotEnabledPage({ appLabel: string })`; `AppLayout({ app: ModuleKey })`; every app layout route declares `staticData: { app }`; the dashboard declares `staticData: { app: "home" }`.

- [ ] **Step 1: Write the failing test**

`apps/host/frontend/src/routes/-app-layout.test.tsx`:

```tsx
import { MantineProvider } from "@mantine/core";
import { render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import "../i18n";
import { AppLayout } from "./-app-layout";

vi.mock("@tanstack/react-router", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@tanstack/react-router")>();
  return {
    ...actual,
    Outlet: () => <div>app content</div>,
    Link: ({ children }: { children: React.ReactNode }) => <a href="/dashboard">{children}</a>,
    useRouter: () => ({ history: { back: vi.fn() } }),
  };
});

const inject = (modules: string[]) => {
  window.__VANTIGO_APP__ = { basePath: "/", title: "Vantigo", support: {}, modules };
};

afterEach(() => {
  delete window.__VANTIGO_APP__;
});

const renderLayout = () =>
  render(
    <MantineProvider>
      <AppLayout app="customers" />
    </MantineProvider>,
  );

describe("AppLayout", () => {
  it("renders the app's routes when its module is enabled", () => {
    inject(["customers", "energy"]);
    renderLayout();
    expect(screen.getByText("app content")).toBeInTheDocument();
  });

  it("renders every module's routes when nothing was injected (dev server)", () => {
    renderLayout();
    expect(screen.getByText("app content")).toBeInTheDocument();
  });

  it("renders the not-enabled page, naming the app, when its module is off", () => {
    inject(["energy"]);
    renderLayout();
    expect(screen.getByRole("heading", { name: "Customers is not enabled" })).toBeInTheDocument();
    expect(screen.getByText(/not enabled in this installation/i)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Go to dashboard" })).toHaveAttribute("href", "/dashboard");
    expect(screen.queryByText("app content")).not.toBeInTheDocument();
  });
});
```

- [ ] **Step 2: Run it to verify it fails**

Run: `bun run --cwd apps/host/frontend test src/routes/-app-layout.test.tsx`
Expected: FAIL, cannot resolve `./-app-layout`.

- [ ] **Step 3: Add the error strings and the page**

`apps/host/frontend/src/catalogs/error.ts`, add to `en`:

```ts
  moduleNotEnabledTitle: "{{app}} is not enabled",
  moduleNotEnabledBody: "This module is not enabled in this installation. Contact your administrator.",
  errorDashboard: "Go to dashboard",
```

and to `nb`:

```ts
  moduleNotEnabledTitle: "{{app}} er ikke aktivert",
  moduleNotEnabledBody: "Denne modulen er ikke aktivert i denne installasjonen. Kontakt administratoren din.",
  errorDashboard: "Gå til kontrollpanelet",
```

`apps/host/frontend/src/components/errors/module-not-enabled.tsx`:

```tsx
import { Button } from "@mantine/core";
import { Link } from "@tanstack/react-router";
import { useI18n } from "@vantigo/frontend-shell";
import { ErrorPage } from "./error-page";
import { ForbiddenIllustration } from "./illustrations";

/**
 * Shown in place of an app whose module the installation turned off
 * (MODULES). Enablement is an installation property, so unlike the forbidden
 * page there is nothing the user can be granted; the only way out is Home.
 */
export const ModuleNotEnabledPage = ({ appLabel }: { appLabel: string }) => {
  const { t } = useI18n("host");
  return (
    <ErrorPage
      illustration={<ForbiddenIllustration />}
      title={t("moduleNotEnabledTitle", { app: appLabel })}
      message={t("moduleNotEnabledBody")}
      actions={
        <Button component={Link} to="/dashboard">
          {t("errorDashboard")}
        </Button>
      }
    />
  );
};
```

Add `export * from "./module-not-enabled";` to `apps/host/frontend/src/components/errors/index.ts` (alphabetical, after `maintenance`).

- [ ] **Step 4: Add the shared layout component**

`apps/host/frontend/src/routes/-app-layout.tsx`:

```tsx
import { Outlet } from "@tanstack/react-router";
import { useI18n } from "@vantigo/frontend-shell";
import { appForKey, isAppEnabled } from "../apps";
import { ModuleNotEnabledPage } from "../components/errors";
import { enabledModuleKeys } from "../lib/enabled-modules";
import type { ModuleKey } from "../navigation";
import "../i18n";

/**
 * The body of every app layout route: the app's routes when its module is
 * enabled, the not-enabled page otherwise. A component check rather than a
 * beforeLoad redirect, so the URL stays put and the page can name the app.
 */
export const AppLayout = ({ app }: { app: ModuleKey }) => {
  const { t } = useI18n("host");
  const definition = appForKey(app);
  if (!isAppEnabled(definition, enabledModuleKeys())) return <ModuleNotEnabledPage appLabel={t(definition.label)} />;
  return <Outlet />;
};
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `bun run --cwd apps/host/frontend test src/routes/-app-layout.test.tsx`
Expected: PASS.

- [ ] **Step 6: Add the four layout routes and tag the dashboard**

`apps/host/frontend/src/routes/customers.tsx`:

```tsx
import { createFileRoute } from "@tanstack/react-router";
import { AppLayout } from "./-app-layout";

export const Route = createFileRoute("/customers")({
  staticData: { app: "customers" },
  component: () => <AppLayout app="customers" />,
});
```

`apps/host/frontend/src/routes/communications.tsx`:

```tsx
import { createFileRoute } from "@tanstack/react-router";
import { AppLayout } from "./-app-layout";

export const Route = createFileRoute("/communications")({
  staticData: { app: "communications" },
  component: () => <AppLayout app="communications" />,
});
```

`apps/host/frontend/src/routes/products.tsx`:

```tsx
import { createFileRoute } from "@tanstack/react-router";
import { AppLayout } from "./-app-layout";

export const Route = createFileRoute("/products")({
  staticData: { app: "products" },
  component: () => <AppLayout app="products" />,
});
```

`apps/host/frontend/src/routes/energy.tsx`:

```tsx
import { createFileRoute } from "@tanstack/react-router";
import { AppLayout } from "./-app-layout";

export const Route = createFileRoute("/energy")({
  staticData: { app: "energy" },
  component: () => <AppLayout app="energy" />,
});
```

`apps/host/frontend/src/routes/dashboard.tsx:897`: add `staticData: { app: "home" },` as the first option of `createFileRoute("/dashboard")({ … })`.

- [ ] **Step 7: Gate the customer energy tab**

Replace `apps/host/frontend/src/routes/customers/$customerId.energy.tsx` with:

```tsx
import { createFileRoute, useParams } from "@tanstack/react-router";
import { CustomerEnergyPanel } from "@vantigo/energy-ui";
import { useI18n } from "@vantigo/frontend-shell";
import { ModuleNotEnabledPage } from "../../components/errors";
import { enabledModuleKeys } from "../../lib/enabled-modules";
import "../../i18n";

// Lives in the customers app but calls the energy API, so it gates on the
// energy module itself: a pasted link must not hit an API 404.
const CustomerEnergyRoute = () => {
  const { t } = useI18n("host");
  const { customerId } = useParams({ from: "/customers/$customerId" });
  if (!enabledModuleKeys().includes("energy")) return <ModuleNotEnabledPage appLabel={t("navigation.energy")} />;
  return <CustomerEnergyPanel customerId={customerId} />;
};

export const Route = createFileRoute("/customers/$customerId/energy")({
  component: CustomerEnergyRoute,
});
```

- [ ] **Step 8: Regenerate the route tree and verify**

Run: `(cd apps/host/frontend && bunx vite build) && grep -c "CustomersRouteImport\|CommunicationsRouteImport\|ProductsRouteImport\|EnergyRouteImport" apps/host/frontend/src/routeTree.gen.ts`
Expected: a non-zero count; the four layout routes now parent their folders.

Run: `bun run --cwd apps/host/frontend test && bun run frontend:typecheck && bun run frontend:lint && bun run translations:check`
Expected: all PASS.

- [ ] **Step 9: Commit**

```bash
git add -A apps/host/frontend/src
git commit -m "feat(frontend): add app layout routes that gate on module enablement"
```

---

### Task 6: Shell test setup and the app switcher

**Files:**
- Create: `packages/frontend-shell/vitest.config.ts`, `packages/frontend-shell/src/test/setup.ts`
- Create: `packages/frontend-shell/src/app-switcher.tsx`, `packages/frontend-shell/src/app-switcher.test.tsx`
- Modify: `packages/frontend-shell/src/i18n/catalogs/shell.ts`, `packages/frontend-shell/src/index.ts`

**Interfaces:**
- Produces: `AppSwitcher({ apps: readonly SwitcherApp[] })` with `SwitcherApp = { id: string; label: string; icon: ComponentType<{ size?; stroke? }>; onSelect: () => void; current?: boolean; disabledReason?: string }`; renders nothing when `apps` is empty.

- [ ] **Step 1: Add the shell test infrastructure**

`packages/frontend-shell/vitest.config.ts`:

```ts
import { defineConfig } from "vitest/config";

export default defineConfig({
  test: { environment: "jsdom", setupFiles: ["src/test/setup.ts"] },
});
```

The shell has no `@testing-library/jest-dom` yet: add `"@testing-library/jest-dom": "catalog:"` to `packages/frontend-shell/package.json` `devDependencies` (the root catalog already pins it) and run `bun install`.

`packages/frontend-shell/src/test/setup.ts`:

```ts
import "@testing-library/jest-dom/vitest";

import { cleanup } from "@testing-library/react";
import { afterEach, vi } from "vitest";

afterEach(() => {
  cleanup();
});

// Mantine components rely on browser APIs that jsdom does not implement.
Object.defineProperty(window, "matchMedia", {
  writable: true,
  value: vi.fn().mockImplementation((query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: vi.fn(),
    removeListener: vi.fn(),
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    dispatchEvent: vi.fn(),
  })),
});

class ResizeObserverMock {
  observe = vi.fn();
  unobserve = vi.fn();
  disconnect = vi.fn();
}

window.ResizeObserver = window.ResizeObserver ?? ResizeObserverMock;
window.HTMLElement.prototype.scrollIntoView = window.HTMLElement.prototype.scrollIntoView ?? vi.fn();
```

Run: `bun run --cwd packages/frontend-shell test`
Expected: the existing tests still PASS under the new config.

- [ ] **Step 2: Write the failing switcher test**

`packages/frontend-shell/src/app-switcher.test.tsx`:

```tsx
import { MantineProvider } from "@mantine/core";
import { IconBolt, IconHome, IconUsers } from "@tabler/icons-react";
import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { AppSwitcher } from "./app-switcher";

const open = async () => {
  fireEvent.click(screen.getByRole("button", { name: "Switch app" }));
  return screen.findByRole("dialog");
};

describe("AppSwitcher", () => {
  it("renders nothing without apps", () => {
    render(
      <MantineProvider>
        <AppSwitcher apps={[]} />
      </MantineProvider>,
    );
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
  });

  it("lists tiles, marks the current one and navigates on select", async () => {
    const onSelect = vi.fn();
    render(
      <MantineProvider>
        <AppSwitcher
          apps={[
            { id: "home", label: "Home", icon: IconHome, onSelect: vi.fn(), current: true },
            { id: "customers", label: "Customers", icon: IconUsers, onSelect },
          ]}
        />
      </MantineProvider>,
    );
    await open();

    const home = screen.getByRole("button", { name: "Home" });
    expect(home).toHaveAttribute("aria-current", "true");
    fireEvent.click(home);

    fireEvent.click(screen.getByRole("button", { name: "Customers" }));
    expect(onSelect).toHaveBeenCalledOnce();
  });

  it("shows a disabled app muted with its reason and never selects it", async () => {
    const onSelect = vi.fn();
    render(
      <MantineProvider>
        <AppSwitcher apps={[{ id: "energy", label: "Energy", icon: IconBolt, onSelect, disabledReason: "Not enabled" }]} />
      </MantineProvider>,
    );
    await open();

    const tile = screen.getByRole("button", { name: /Energy/ });
    expect(tile).toHaveAttribute("aria-disabled", "true");
    expect(tile).toBeDisabled();
    expect(screen.getByText("Not enabled")).toBeInTheDocument();
    fireEvent.click(tile);
    expect(onSelect).not.toHaveBeenCalled();
  });
});
```

- [ ] **Step 3: Run it to verify it fails**

Run: `bun run --cwd packages/frontend-shell test src/app-switcher.test.tsx`
Expected: FAIL, cannot resolve `./app-switcher`.

- [ ] **Step 4: Add the strings and the component**

`packages/frontend-shell/src/i18n/catalogs/shell.ts`, add to `en`: `switchApp: "Switch app", appsMenu: "Apps",` and to `nb`: `switchApp: "Bytt app", appsMenu: "Apper",`.

`packages/frontend-shell/src/app-switcher.tsx`:

```tsx
import { ActionIcon, Popover, SimpleGrid, Text, ThemeIcon, Tooltip, UnstyledButton } from "@mantine/core";
import { useDisclosure } from "@mantine/hooks";
import { IconGridDots } from "@tabler/icons-react";
import type { ComponentType } from "react";
import { registerCatalog, useI18n } from "./i18n";
import { shellCatalog } from "./i18n/catalogs/shell";

registerCatalog("shell", shellCatalog);

export interface SwitcherApp {
  /** Stable identifier, e.g. "customers". */
  id: string;
  /** Display name shown in the tile, already translated. */
  label: string;
  icon: ComponentType<{ size?: number | string; stroke?: number | string }>;
  /** Client-side navigation to the app's home. */
  onSelect: () => void;
  /** The app the user is in; rendered selected and inert. */
  current?: boolean;
  /** Present when the app is installed but turned off; shown muted as a caption, not selectable. */
  disabledReason?: string;
}

/**
 * The Google Workspace style application switcher in the header: a waffle
 * icon opening a grid of app tiles. Purely presentational — the host decides
 * which apps appear, which is current and which are disabled.
 */
export const AppSwitcher = ({ apps }: { apps: readonly SwitcherApp[] }) => {
  const [opened, { toggle, close }] = useDisclosure();
  const { t } = useI18n("shell");
  if (apps.length === 0) return null;

  return (
    <Popover opened={opened} onChange={close} position="bottom-end" withArrow shadow="md" width={330}>
      <Popover.Target>
        <Tooltip label={t("appsMenu")} openDelay={500}>
          <ActionIcon variant="subtle" color="gray" size="lg" radius="xl" aria-label={t("switchApp")} onClick={toggle}>
            <IconGridDots size={20} stroke={1.5} />
          </ActionIcon>
        </Tooltip>
      </Popover.Target>
      <Popover.Dropdown p="sm" role="dialog" aria-label={t("appsMenu")}>
        <SimpleGrid cols={Math.min(apps.length, 3)} spacing="xs">
          {apps.map((app) => {
            const disabled = app.disabledReason !== undefined;
            const inert = disabled || app.current === true;
            return (
              <UnstyledButton
                key={app.id}
                component="button"
                type="button"
                disabled={disabled}
                aria-disabled={disabled || undefined}
                aria-current={app.current ? "true" : undefined}
                p="xs"
                onClick={
                  inert
                    ? undefined
                    : () => {
                        close();
                        app.onSelect();
                      }
                }
                style={{
                  display: "flex",
                  flexDirection: "column",
                  alignItems: "center",
                  gap: 6,
                  minWidth: 0,
                  borderRadius: "var(--mantine-radius-md)",
                  background: app.current ? "var(--mantine-primary-color-light)" : undefined,
                  cursor: inert ? "default" : "pointer",
                  opacity: disabled ? 0.5 : undefined,
                }}
              >
                <ThemeIcon size={38} radius="md" variant={app.current ? "filled" : "light"} color={disabled ? "gray" : undefined}>
                  <app.icon size={22} stroke={1.5} />
                </ThemeIcon>
                <Text fz={11} fw={app.current ? 600 : 400} ta="center" w="100%" lineClamp={2} style={{ overflowWrap: "anywhere" }}>
                  {app.label}
                </Text>
                {disabled && (
                  <Text fz={10} c="dimmed" ta="center" w="100%">
                    {app.disabledReason}
                  </Text>
                )}
              </UnstyledButton>
            );
          })}
        </SimpleGrid>
      </Popover.Dropdown>
    </Popover>
  );
};
```

Export from `packages/frontend-shell/src/index.ts`: `export { AppSwitcher, type SwitcherApp } from "./app-switcher";`.

- [ ] **Step 5: Run the test to verify it passes**

Run: `bun run --cwd packages/frontend-shell test src/app-switcher.test.tsx && bun run --cwd packages/frontend-shell typecheck && bun run translations:check`
Expected: PASS. If the popover content is not found, the dropdown is mounted lazily; `findByRole("dialog")` waits for it — do not add `keepMounted`.

- [ ] **Step 6: Commit**

```bash
git add packages/frontend-shell bun.lock
git commit -m "feat(shell): restore the app switcher with a disabled state"
```

---

### Task 7: Account menu and mobile search trigger

**Files:**
- Create: `packages/frontend-shell/src/account-menu.tsx`, `packages/frontend-shell/src/account-menu.test.tsx`
- Create: `packages/frontend-shell/src/spotlight-search-button.tsx`
- Modify: `packages/frontend-shell/src/i18n/catalogs/shell.ts`, `packages/frontend-shell/src/index.ts`

**Interfaces:**
- Produces: `ShellUser` (moved here from `app-shell-layout.tsx`, same shape `{ displayName; email; avatarUrl? }`); `AccountMenu({ user, sections, onSignOut, signOutDisabled? })` with `AccountMenuSection = { label: string; items: AccountMenuItem[] }` and `AccountMenuItem = { label: string; icon: ComponentType<{ size?; stroke? }>; onSelect: () => void }`; `SpotlightSearchButton()`.

- [ ] **Step 1: Write the failing test**

`packages/frontend-shell/src/account-menu.test.tsx`:

```tsx
import { MantineProvider } from "@mantine/core";
import { IconSettings, IconShield } from "@tabler/icons-react";
import { fireEvent, render, screen, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { AccountMenu } from "./account-menu";

const user = { displayName: "Ada Lovelace", email: "ada@acme.test" };

const open = async () => {
  fireEvent.click(screen.getByRole("button", { name: "Open account menu" }));
  return screen.findByRole("menu");
};

describe("AccountMenu", () => {
  it("shows the user, the given sections in order, and sign out last", async () => {
    const onSettings = vi.fn();
    const onSignOut = vi.fn();
    render(
      <MantineProvider>
        <AccountMenu
          user={user}
          sections={[
            { label: "Your account", items: [{ label: "Settings", icon: IconSettings, onSelect: onSettings }] },
            { label: "Workspace", items: [{ label: "Roles and access", icon: IconShield, onSelect: vi.fn() }] },
          ]}
          onSignOut={onSignOut}
        />
      </MantineProvider>,
    );
    expect(screen.getByText("AL")).toBeInTheDocument();

    const menu = await open();
    expect(within(menu).getByText("Ada Lovelace")).toBeInTheDocument();
    expect(within(menu).getByText("ada@acme.test")).toBeInTheDocument();
    expect(within(menu).getAllByRole("menuitem").map((item) => item.textContent)).toEqual([
      "Settings",
      "Roles and access",
      "Sign out",
    ]);
    const labels = within(menu).getAllByText(/Your account|Workspace/).map((node) => node.textContent);
    expect(labels).toEqual(["Your account", "Workspace"]);

    fireEvent.click(within(menu).getByRole("menuitem", { name: "Settings" }));
    expect(onSettings).toHaveBeenCalledOnce();
  });

  it("signs out, unless sign-out is disabled", async () => {
    const onSignOut = vi.fn();
    const { rerender } = render(
      <MantineProvider>
        <AccountMenu user={user} sections={[]} onSignOut={onSignOut} />
      </MantineProvider>,
    );
    let menu = await open();
    fireEvent.click(within(menu).getByRole("menuitem", { name: "Sign out" }));
    expect(onSignOut).toHaveBeenCalledOnce();

    rerender(
      <MantineProvider>
        <AccountMenu user={user} sections={[]} onSignOut={onSignOut} signOutDisabled />
      </MantineProvider>,
    );
    menu = await open();
    expect(within(menu).getByRole("menuitem", { name: "Sign out" })).toHaveAttribute("data-disabled");
  });

  it("renders a placeholder while the user is loading", () => {
    render(
      <MantineProvider>
        <AccountMenu user={undefined} sections={[]} onSignOut={vi.fn()} />
      </MantineProvider>,
    );
    expect(screen.getByRole("button", { name: "Open account menu" })).toBeInTheDocument();
  });
});
```

- [ ] **Step 2: Run it to verify it fails**

Run: `bun run --cwd packages/frontend-shell test src/account-menu.test.tsx`
Expected: FAIL, cannot resolve `./account-menu`.

- [ ] **Step 3: Add the strings and both components**

`packages/frontend-shell/src/i18n/catalogs/shell.ts`, add to `en`: `openSearch: "Open search",` and to `nb`: `openSearch: "Åpne søk",`.

`packages/frontend-shell/src/account-menu.tsx`:

```tsx
import { Avatar, Box, Menu, Text, UnstyledButton } from "@mantine/core";
import { IconLogout } from "@tabler/icons-react";
import { type ComponentType, Fragment } from "react";
import { registerCatalog, useI18n } from "./i18n";
import { shellCatalog } from "./i18n/catalogs/shell";

registerCatalog("shell", shellCatalog);

export interface ShellUser {
  displayName: string;
  email: string;
  avatarUrl?: string | null;
}

export interface AccountMenuItem {
  /** Already translated. */
  label: string;
  icon: ComponentType<{ size?: number | string; stroke?: number | string }>;
  onSelect: () => void;
}

export interface AccountMenuSection {
  /** Already translated section heading. */
  label: string;
  items: readonly AccountMenuItem[];
}

export interface AccountMenuProps {
  /** The signed-in user; undefined while loading. */
  user: ShellUser | undefined;
  /** Sections to show, already filtered to what the user may see. Empty sections should not be passed. */
  sections: readonly AccountMenuSection[];
  onSignOut: () => void;
  signOutDisabled?: boolean;
}

const initials = (name: string) =>
  name
    .split(/\s+/)
    .map((part) => part[0])
    .join("")
    .slice(0, 2)
    .toUpperCase();

/**
 * The avatar menu in the header: who is signed in, the personal and
 * administrative destinations grouped under headings, and sign out.
 */
export const AccountMenu = ({ user, sections, onSignOut, signOutDisabled }: AccountMenuProps) => {
  const { t } = useI18n("shell");
  return (
    <Menu position="bottom-end" withArrow width={260}>
      <Menu.Target>
        <UnstyledButton
          aria-label={t("openAccountMenu")}
          aria-haspopup="menu"
          styles={{
            root: {
              borderRadius: "50%",
              "&:focus-visible": { outline: "2px solid var(--mantine-primary-color-filled)", outlineOffset: 2 },
            },
          }}
        >
          <Avatar src={user?.avatarUrl} color="vantigo" radius="xl">
            {user ? initials(user.displayName) : t("loadingIndicator")}
          </Avatar>
        </UnstyledButton>
      </Menu.Target>
      <Menu.Dropdown>
        <Box px="sm" py="xs">
          <Text size="sm" fw={500} truncate>
            {user?.displayName ?? t("loadingAccount")}
          </Text>
          <Text size="xs" c="dimmed" truncate>
            {user?.email ?? ""}
          </Text>
        </Box>
        {sections.map((section) => (
          <Fragment key={section.label}>
            <Menu.Divider />
            <Menu.Label>{section.label}</Menu.Label>
            {section.items.map((item) => (
              <Menu.Item key={item.label} leftSection={<item.icon size={14} />} onClick={item.onSelect}>
                {item.label}
              </Menu.Item>
            ))}
          </Fragment>
        ))}
        <Menu.Divider />
        <Menu.Item color="red" leftSection={<IconLogout size={14} />} onClick={onSignOut} disabled={signOutDisabled}>
          {t("signOut")}
        </Menu.Item>
      </Menu.Dropdown>
    </Menu>
  );
};
```

`packages/frontend-shell/src/spotlight-search-button.tsx`:

```tsx
import { ActionIcon } from "@mantine/core";
import { spotlight } from "@mantine/spotlight";
import { IconSearch } from "@tabler/icons-react";
import { registerCatalog, useI18n } from "./i18n";
import { shellCatalog } from "./i18n/catalogs/shell";

registerCatalog("shell", shellCatalog);

/**
 * The narrow-viewport search trigger: an icon in the header's action group
 * that opens the same spotlight the desktop search box does. Hidden from the
 * `sm` breakpoint up, where the search box is visible instead.
 */
export const SpotlightSearchButton = () => {
  const { t } = useI18n("shell");
  return (
    <ActionIcon variant="subtle" color="gray" size="lg" radius="xl" hiddenFrom="sm" aria-label={t("openSearch")} onClick={spotlight.open}>
      <IconSearch size={20} stroke={1.5} />
    </ActionIcon>
  );
};
```

In `packages/frontend-shell/src/index.ts` add:

```ts
export { AccountMenu, type AccountMenuItem, type AccountMenuProps, type AccountMenuSection, type ShellUser } from "./account-menu";
export { SpotlightSearchButton } from "./spotlight-search-button";
```

and remove `type ShellUser` from the `./app-shell-layout` export line. In `app-shell-layout.tsx`, delete the local `ShellUser` interface and add `import type { ShellUser } from "./account-menu";` (the layout still uses it until Task 8 removes the prop).

- [ ] **Step 4: Run the tests to verify they pass**

Run: `bun run --cwd packages/frontend-shell test && bun run --cwd packages/frontend-shell typecheck && bun run translations:check`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add packages/frontend-shell
git commit -m "feat(shell): add the header account menu and a mobile search trigger"
```

---

### Task 8: Header slots, optional sidebar, and the root composition

**Files:**
- Modify: `packages/frontend-shell/src/app-shell-layout.tsx` (rewrite the component), `packages/frontend-shell/src/spotlight-search-box.tsx:10-14` (doc comment), `packages/frontend-shell/src/index.ts`
- Create: `packages/frontend-shell/src/app-shell-layout.test.tsx`
- Modify: `apps/customers/frontend/src/components/app-shell-whitelabel.test.tsx:11-22`, `apps/products/frontend/src/components/app-shell-whitelabel.test.tsx:11-22`
- Modify: `apps/host/frontend/src/routes/__root.tsx` (rewrite `RootLayout`)

**Interfaces:**
- Consumes: `AppSwitcher`, `AccountMenu`, `SpotlightSearchButton`, `SpotlightSearchBox` from the shell; `activeAppKey`, `appForKey`, `isAppEnabled`, `switcherTiles` from `apps.ts`; `accountMenuSections`; `enabledModuleKeys`; `visibleNavSections`.
- Produces: `AppShellLayoutProps = { title?: string; headerCenter?: ReactNode; headerActions?: ReactNode; nav?: (close) => ReactNode; overlay?: (close) => ReactNode; children }`. Removed props: `moduleName`, `user`, `userMenuItems`, `onSignOut`, `signOutDisabled`, `navbarTop`, `navLower`.

- [ ] **Step 1: Write the failing shell layout test**

`packages/frontend-shell/src/app-shell-layout.test.tsx`:

```tsx
import { MantineProvider } from "@mantine/core";
import { render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { AppShellLayout } from "./app-shell-layout";

afterEach(() => {
  delete window.__VANTIGO_APP__;
});

describe("AppShellLayout", () => {
  it("renders no sidebar and no burger when no nav is given, and the content still", () => {
    render(
      <MantineProvider>
        <AppShellLayout>
          <div>content</div>
        </AppShellLayout>
      </MantineProvider>,
    );
    expect(screen.queryByRole("navigation", { name: "Primary navigation" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Open navigation" })).not.toBeInTheDocument();
    expect(screen.getByText("content")).toBeInTheDocument();
  });

  it("renders the sidebar and the burger when nav is given", () => {
    render(
      <MantineProvider>
        <AppShellLayout nav={() => <a href="/customers">Customers</a>}>
          <div>content</div>
        </AppShellLayout>
      </MantineProvider>,
    );
    expect(screen.getByRole("navigation", { name: "Primary navigation" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Open navigation" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Customers" })).toBeInTheDocument();
  });

  it("places the header slots in the banner and shows the app title over the product title", () => {
    window.__VANTIGO_APP__ = { basePath: "/", title: "Acme ERP", support: {} };
    render(
      <MantineProvider>
        <AppShellLayout title="Customers" headerCenter={<span>search here</span>} headerActions={<button type="button">act</button>}>
          <div>content</div>
        </AppShellLayout>
      </MantineProvider>,
    );
    const header = screen.getByRole("banner");
    expect(header).toHaveTextContent("Customers");
    expect(screen.queryByRole("heading", { name: "Acme ERP" })).not.toBeInTheDocument();
    expect(header).toHaveTextContent("search here");
    expect(header).toContainElement(screen.getByRole("button", { name: "act" }));
  });
});
```

- [ ] **Step 2: Run it to verify it fails**

Run: `bun run --cwd packages/frontend-shell test src/app-shell-layout.test.tsx`
Expected: FAIL (type errors on unknown props / missing `nav`).

- [ ] **Step 3: Rewrite the shell layout component**

In `packages/frontend-shell/src/app-shell-layout.tsx`, replace the imports, the props interface and the `AppShellLayout` component (keep `SupportContactLine` and `registerCatalog` as they are):

```tsx
import { Anchor, AppShell, Box, Burger, Divider, Group, Image, Text, Title } from "@mantine/core";
import { useDisclosure } from "@mantine/hooks";
import type { ReactNode } from "react";
import { appConfig, hasSupportContact } from "./app-config";
import { registerCatalog, useI18n } from "./i18n";
import { shellCatalog } from "./i18n/catalogs/shell";
import { vantigoLogo } from "./logo";

registerCatalog("shell", shellCatalog);

export interface AppShellLayoutProps {
  /** Shown next to the logo (the active app's name). Defaults to the runtime app title. */
  title?: string;
  /** Header center slot, e.g. the spotlight search box. Hidden below the `sm` breakpoint. */
  headerCenter?: ReactNode;
  /** Header right slot: the mobile search trigger, the app switcher, the account menu. */
  headerActions?: ReactNode;
  /** The sidebar navigation. Omit to render no sidebar and no burger. Call `closeMobileNav` when a nav item is clicked. */
  nav?: (closeMobileNav: () => void) => ReactNode;
  /** Optional overlay rendered with access to the mobile-nav close callback. */
  overlay?: (closeMobileNav: () => void) => ReactNode;
  children: ReactNode;
}
```

```tsx
/**
 * The shared authenticated application shell: logo and app name on the left
 * of the header, search in the center, the app switcher and account menu on
 * the right; the active app's navigation in the sidebar (when it has one);
 * and — when support contact details are configured — a slim support footer.
 */
export const AppShellLayout = ({ title, headerCenter, headerActions, nav, overlay, children }: AppShellLayoutProps) => {
  const [opened, { toggle, close }] = useDisclosure();
  const { t } = useI18n("shell");
  const config = appConfig();
  const showFooter = hasSupportContact(config);

  return (
    <AppShell
      header={{ height: 60 }}
      navbar={nav ? { width: 260, breakpoint: "sm", collapsed: { mobile: !opened } } : undefined}
      footer={showFooter ? { height: 36 } : undefined}
      padding="md"
    >
      <AppShell.Header>
        <Group h="100%" px="md" gap="sm" wrap="nowrap">
          {nav && (
            <Burger
              opened={opened}
              onClick={toggle}
              hiddenFrom="sm"
              size="sm"
              aria-label={t(opened ? "closeNavigation" : "openNavigation")}
            />
          )}
          <Image src={config.logoUrl ?? vantigoLogo} alt={config.title} h={32} w="auto" maw="30vw" fit="contain" />
          <Divider orientation="vertical" my="md" />
          <Title
            order={4}
            fw={500}
            style={{ minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}
          >
            {title ?? config.title}
          </Title>
          {headerCenter && (
            <Box flex={1} visibleFrom="sm" px="md">
              <Box maw={480} mx="auto">
                {headerCenter}
              </Box>
            </Box>
          )}
          <Group gap="xs" wrap="nowrap" ml="auto">
            {headerActions}
          </Group>
        </Group>
      </AppShell.Header>

      {nav && (
        <AppShell.Navbar
          p="md"
          component="nav"
          aria-label={t("primaryNavigation")}
          style={{ minHeight: 0, overflowY: "auto", overscrollBehavior: "contain" }}
        >
          <AppShell.Section grow style={{ flex: "1 0 auto" }}>
            {nav(close)}
          </AppShell.Section>
        </AppShell.Navbar>
      )}

      <AppShell.Main>{children}</AppShell.Main>

      {overlay?.(close)}

      {showFooter && (
        <AppShell.Footer>
          <Group h="100%" px="md" justify="center">
            <SupportContactLine />
          </Group>
        </AppShell.Footer>
      )}
    </AppShell>
  );
};
```

Delete the `initials` helper and the `ShellUser` import from this file (both now live in `account-menu.tsx`). Update the doc comment in `spotlight-search-box.tsx` to say "Place it in the header via the AppShellLayout `headerCenter` slot".

- [ ] **Step 4: Update the two whitelabel tests**

In both `apps/customers/frontend/src/components/app-shell-whitelabel.test.tsx` and `apps/products/frontend/src/components/app-shell-whitelabel.test.tsx`, replace the `renderShell` helper with:

```tsx
const renderShell = () =>
  render(
    <MantineProvider>
      <AppShellLayout nav={() => null}>
        <div>content</div>
      </AppShellLayout>
    </MantineProvider>,
  );
```

- [ ] **Step 5: Run the shell and module tests**

Run: `bun run --cwd packages/frontend-shell test && bun run --cwd apps/customers/frontend test src/components/app-shell-whitelabel.test.tsx && bun run --cwd apps/products/frontend test src/components/app-shell-whitelabel.test.tsx`
Expected: PASS. (The host does not type-check yet; that is the next step.)

- [ ] **Step 6: Rewrite the root layout**

Replace `apps/host/frontend/src/routes/__root.tsx` from the imports through the end of `RootLayout` (leave `Route` as is) with:

```tsx
import { Alert, Center, Loader, NavLink, Stack, Text } from "@mantine/core";
import type { QueryClient } from "@tanstack/react-query";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ReactQueryDevtools } from "@tanstack/react-query-devtools";
import {
  createRootRouteWithContext,
  Link,
  Outlet,
  redirect,
  useMatches,
  useNavigate,
  useRouterState,
} from "@tanstack/react-router";
import { TanStackRouterDevtools } from "@tanstack/react-router-devtools";
import {
  AccountMenu,
  AppShellLayout,
  AppSwitcher,
  appUrl,
  SpotlightSearchBox,
  SpotlightSearchButton,
  useI18n,
} from "@vantigo/frontend-shell";
import { useEffect, useState } from "react";
import "../i18n";
import { accountMenuSections } from "../account-menu";
import { getProfile, profileQueryKey } from "../api/account";
import { fetchBootstrapStatus } from "../api/account-lifecycle";
import { fetchSession, sessionQueryKey, signOut } from "../api/auth";
import { getAuthorizationMe } from "../api/authorization";
import { fetchSystemStatus, shouldShowMaintenance, systemStatusQueryKey } from "../api/system-status";
import { activeAppKey, appForKey, isAppEnabled, switcherTiles } from "../apps";
import { AppSpotlight } from "../components/app-spotlight";
import { MaintenancePage } from "../components/errors";
import { ModuleAccessGuard } from "../components/module-access-guard";
import { enabledModuleKeys } from "../lib/enabled-modules";
import { publicPaths } from "../lib/public-paths";
import { activeNavPath, type NavSection, visibleNavSections } from "../navigation";

const renderNavSections = (
  sections: readonly NavSection[],
  pathname: string,
  close: () => void,
  t: (key: string) => string,
) =>
  sections.map((section, sectionIndex) => {
    if (section.items.length === 0) return null;
    const active = activeNavPath(pathname, section.items);
    return (
      <div key={section.label ?? sectionIndex}>
        {section.label && (
          <Text size="xs" fw={700} tt="uppercase" c="dimmed" mt="md" mb={4} px="xs">
            {t(section.label)}
          </Text>
        )}
        {section.items.map((item) => (
          <NavLink
            key={item.to}
            component={Link}
            to={item.to}
            // Mantine styles [aria-current="page"] as active; keep TanStack's
            // own marker exact so only activeNavPath decides the highlight.
            activeOptions={{ exact: true }}
            label={t(item.label)}
            leftSection={<item.icon size={18} stroke={1.5} />}
            active={item.to === active}
            onClick={close}
          />
        ))}
      </div>
    );
  });

const RootLayout = () => {
  const { t } = useI18n("host");
  const location = useRouterState({ select: (state) => state.location });
  const pathname = location.pathname;
  const matches = useMatches();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [maintenanceWarningDismissed, setMaintenanceWarningDismissed] = useState(false);
  const isPublic = publicPaths.has(pathname);
  const { data: session, isPending } = useQuery({
    queryKey: sessionQueryKey,
    queryFn: fetchSession,
    enabled: !isPublic,
    staleTime: 300_000,
  });
  const systemStatus = useQuery({
    queryKey: systemStatusQueryKey,
    queryFn: fetchSystemStatus,
    staleTime: 30_000,
    refetchInterval: 30_000,
  });
  const authorization = useQuery({
    queryKey: ["authorization", "me", "none"],
    queryFn: getAuthorizationMe,
    enabled: !isPublic && !!session,
    retry: false,
    staleTime: 300_000,
  });
  const profile = useQuery({
    queryKey: profileQueryKey(session?.user.id ?? "unknown"),
    queryFn: getProfile,
    enabled: !isPublic && !!session,
    staleTime: 300_000,
  });
  const logout = useMutation({
    mutationFn: signOut,
    onSuccess: () => {
      queryClient.setQueryData(sessionQueryKey, null);
      window.location.assign(appUrl("/sign-in"));
    },
  });
  useEffect(() => {
    if (!isPublic && !isPending && !session) window.location.assign(appUrl("/sign-in"));
  }, [isPending, isPublic, session]);
  if (isPublic) return <Outlet />;
  if (isPending || !session)
    return (
      <Center mih="100vh">
        <Loader size="sm" />
      </Center>
    );
  const isOwner = session.user.roles.includes("Owner");
  const permissions = authorization.data?.permissions;
  const canManageAuthorization = authorization.data?.canManageAuthorization === true;
  const enabledModules = enabledModuleKeys();
  const visibility = {
    permissions,
    isOwner,
    canManageAuthorization,
    isSystemAdmin: session.isSystemAdmin,
    enabledModules,
  };
  // The app is whatever the deepest matched route declares; administration
  // and public paths declare none and render sidebar-less.
  const activeKey = activeAppKey(matches);
  const activeApp = activeKey ? appForKey(activeKey) : undefined;
  const navSections =
    activeApp && isAppEnabled(activeApp, enabledModules) ? visibleNavSections(activeApp.navSections, visibility) : [];
  const menuSections = visibleNavSections(accountMenuSections, visibility);
  // Sidebar links and the registry use bare path strings, as the nav catalog
  // always has; the router validates search params at runtime.
  const go = (to: string) => void navigate({ to: to as never });
  if (shouldShowMaintenance(systemStatus.data, session.isSystemAdmin)) {
    return <MaintenancePage message={systemStatus.data?.message} />;
  }
  return (
    <>
      <AppShellLayout
        title={activeApp && activeApp.key !== "home" ? t(activeApp.label) : undefined}
        headerCenter={<SpotlightSearchBox />}
        headerActions={
          <>
            <SpotlightSearchButton />
            <AppSwitcher
              apps={switcherTiles(permissions, enabledModules, activeKey).map((tile) => ({
                id: tile.app.key,
                label: t(tile.app.label),
                icon: tile.app.icon,
                current: tile.current,
                disabledReason: tile.enabled ? undefined : t("navigation.notEnabled"),
                onSelect: () => go(tile.app.home),
              }))}
            />
            <AccountMenu
              user={{
                ...session.user,
                avatarUrl: profile.data?.avatarUrl
                  ? `${appUrl(profile.data.avatarUrl)}${profile.data.avatarUrl.includes("?") ? "&" : "?"}v=${profile.dataUpdatedAt}`
                  : null,
              }}
              sections={menuSections.map((section) => ({
                label: t(section.label),
                items: section.items.map((item) => ({ label: t(item.label), icon: item.icon, onSelect: () => go(item.to) })),
              }))}
              onSignOut={() => logout.mutate()}
              signOutDisabled={logout.isPending}
            />
          </>
        }
        nav={navSections.length > 0 ? (close) => renderNavSections(navSections, pathname, close, t) : undefined}
      >
        <Stack gap="md">
          {session.isSystemAdmin && systemStatus.data?.maintenance && !maintenanceWarningDismissed && (
            <Alert
              color="yellow"
              title={t("systemAdmin.maintenanceActive")}
              withCloseButton
              onClose={() => setMaintenanceWarningDismissed(true)}
            >
              {systemStatus.data.message || t("systemAdmin.maintenanceActiveBody")}
            </Alert>
          )}
          <ModuleAccessGuard>
            <Outlet />
          </ModuleAccessGuard>
        </Stack>
      </AppShellLayout>
      <AppSpotlight
        permissions={permissions}
        isOwner={isOwner}
        canManageAuthorization={canManageAuthorization}
        isSystemAdmin={session.isSystemAdmin}
        enabledModules={enabledModules}
      />
      <TanStackRouterDevtools />
      <ReactQueryDevtools />
    </>
  );
};
```

- [ ] **Step 7: Type-check, lint, test everything frontend**

Run: `bun run frontend:typecheck && bun run frontend:lint && bun run translations:check && bun run format:check && bun run frontend:test`
Expected: all PASS. If biome reports formatting, run `bun run format:write` and re-check.

- [ ] **Step 8: Look at it in the browser**

Run the backend and the Vite dev server (see CONTRIBUTING "Frontend development": dev server on :10011 proxying `/api` to :8080), sign in, and confirm:

- Header: logo, product title on `/dashboard`, search box centered, waffle and avatar on the right; no sidebar on `/dashboard`, `/settings`, `/workspace/overview`.
- `/customers`: header title "Customers", sidebar with Customers and Contacts, contacts at `/customers/contacts`.
- Waffle: Home, Customers, Communications, Products, Energy; the current one highlighted; a tile navigates.
- Avatar menu: name/email, "Your account · Settings", the workspace and system groups only for the right roles, "Sign out".
- Restart the server with `MODULES=customers,products`: the Communications and Energy tiles are muted with "Not enabled"; `/energy/metering-points` shows "Energy is not enabled" without a sidebar; the customer detail page shows no Energy tab and `/customers/1/energy` shows the not-enabled page.
- Narrow the window below 768px: the search box disappears, the search icon appears and opens the spotlight; the burger only shows inside an app.

- [ ] **Step 9: Commit**

```bash
git add -A packages/frontend-shell apps/host/frontend/src apps/customers/frontend/src apps/products/frontend/src
git commit -m "feat(frontend): per-app sidebar with the app switcher and account menu in the header"
```

---

### Task 9: Documentation, full gates, pull request

**Files:**
- Modify: `CONTRIBUTING.md:143-164`
- Modify: `docs/module-boundaries.md:99-100`

- [ ] **Step 1: Update CONTRIBUTING**

Replace the "SPA URL convention" paragraph (starting "SPA URL convention — *flat primary resources…*" through "…not the SPA paths.") with:

```markdown
SPA URL convention — *one prefix per app*: every route of a business module
lives under its module's name, which is also its API prefix and its `MODULES`
entry (`/customers`, `/customers/contacts`, `/communications/inbox`,
`/products/categories`, `/energy/metering-points`). Nesting inside the prefix
means *belonging* (`/customers/:id`). The dashboard (`/dashboard`) is the
"Home" app; `/settings`, `/workspace` and `/admin` are administration pages
reached from the avatar menu and belong to no app. Each app is declared in
`apps/host/frontend/src/apps.ts` (label, icon, home, sidebar entries) and has a
layout route at its prefix (`routes/<app>.tsx`) that tags the subtree with the
app key and renders the not-enabled page when the module is off. Backend API
routes keep the same module prefix (`/api/v1/customers/contacts`).
```

Also change "Route files in `apps/host/frontend/src/routes/` are thin wrappers that lazy-import module pages" to "…thin wrappers around module pages; the router plugin's `autoCodeSplitting` makes each route its own chunk".

- [ ] **Step 2: Update module-boundaries**

Replace step 6 of "Adding a module" with:

```markdown
6. For a frontend package, copy the `no-restricted-imports` block into its
   `eslint.config.js`, add the module key to `moduleKeys` in
   `apps/host/frontend/src/navigation.ts`, register the app (label, icon,
   home path, sidebar entries) in `apps/host/frontend/src/apps.ts`, and add a
   layout route `apps/host/frontend/src/routes/<name>.tsx` that renders
   `<AppLayout app="<name>" />`.
```

- [ ] **Step 3: Run every gate**

```bash
bun run translations:check && bun run i18n:test && bun run format:check
bun run frontend:lint && bun run frontend:typecheck && bun run frontend:test
(cd apps/server && gofmt -l . && go vet ./... && taskset -c 0-3 go test -race -count=1 ./...)
bun run build
```

Expected: every command exits 0. Report any failure verbatim; do not open the PR on red.

- [ ] **Step 4: Commit and open the PR**

```bash
git add CONTRIBUTING.md docs/module-boundaries.md
git commit -m "docs(frontend): describe the per-app URL convention and registry"
git push -u origin feat/app-switcher
gh pr create --title "feat(frontend): app switcher and per-app shell" --body-file - <<'EOF'
## Summary

- Each business module is now an "app" under its own URL prefix (`/customers/contacts`, `/communications/inbox` moved), with its own sidebar. A waffle app switcher and an avatar account menu sit in the header, Gmail-style; settings, workspace and system admin moved into the avatar menu and render without a sidebar, as does the dashboard.
- Module enablement (`MODULES`) is injected into the page by the server; disabled apps show muted with "Not enabled", and their routes render a not-enabled page.
- Spec: `docs/superpowers/specs/2026-09-16-app-switcher-design.md`. Plan: `docs/superpowers/plans/2026-09-16-app-switcher.md`.

## Test plan

- [ ] `bun run frontend:test`, `frontend:typecheck`, `frontend:lint`, `translations:check`, `format:check` green
- [ ] `go test -race ./...` green
- [ ] Manual: switcher, account menu, per-app sidebar, `MODULES=customers,products` shows Communications/Energy as not enabled

🤖 Generated with [Claude Code](https://claude.com/claude-code)

https://claude.ai/code/session_01LCSZSNLSNnXbV1JTcw7Xar
EOF
```

If `gh pr edit` is needed later, use `gh api -X PATCH repos/{owner}/{repo}/pulls/<n>` (the `edit` subcommand is broken in this environment).
