import {
  IconBarcode,
  IconBuildingFactory2,
  IconBuildingWarehouse,
  IconBulb,
  IconCalendarDue,
  IconCalendarEvent,
  IconCalendarStats,
  IconCalendarTime,
  IconClipboardCheck,
  IconClipboardList,
  IconFileImport,
  IconHammer,
  IconListDetails,
  IconPackageImport,
  IconPuzzle,
  IconTool,
  IconTransfer,
  IconTrendingDown,
  IconTruckDelivery,
  IconUsersGroup,
  IconZoomExclamation,
} from "@tabler/icons-react";
import { inventoryNavItems } from "@vantigo/inventory-ui";
import { manufacturingNavItems } from "@vantigo/manufacturing-ui";
import { planningNavItems } from "@vantigo/planning-ui";
import { staffNavItems } from "@vantigo/staff-ui";
import { describe, expect, it } from "vitest";
import { accountMenuSections } from "./account-menu";
import {
  activeAppKey,
  allNavSections,
  appForKey,
  appNavSections,
  apps,
  appTitleLabel,
  areaForKey,
  areas,
  isAppEnabled,
  moduleLabelKey,
  spotlightNavSections,
  switcherTiles,
} from "./apps";
import { navigationCatalog } from "./catalogs/navigation";
import { connectionProviders } from "./lib/connections";
import { type ModuleKey, type NavVisibilityContext, visibleNavSections } from "./navigation";

const allModules = [
  "assistant",
  "calendar",
  "communications",
  "customers",
  "energy",
  "entra",
  "expenses",
  "google",
  "inventory",
  "invoices",
  "planning",
  "products",
  "projects",
  "staff",
  "time",
] as const;

describe("the app registry", () => {
  it("lists Home first, without a module, and every module app once", () => {
    expect(apps[0]).toMatchObject({ key: "home", home: "/dashboard", navSections: [] });
    expect(apps[0]?.module).toBeUndefined();
    expect(apps.map((app) => app.key)).toEqual([
      "home",
      "assistant",
      "calendar",
      "customers",
      "projects",
      "time",
      "expenses",
      "staff",
      "planning",
      "invoices",
      "orders",
      "communications",
      "knowledge",
      "agents",
      "products",
      "inventory",
      "manufacturing",
      "energy",
    ]);
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
      "/calendar",
      "/calendar/settings",
      "/customers",
      "/customers/contacts",
      "/customers/follow-ups",
      "/projects",
      "/projects/my-tasks",
      "/projects/economy",
      "/projects/templates",
      "/time",
      "/time/approvals",
      "/time/people",
      "/time/settings",
      "/expenses",
      "/expenses/approvals",
      "/expenses/reimbursements",
      "/expenses/settings",
      "/staff/me",
      "/staff/people",
      "/staff/absence",
      "/staff/competence",
      "/staff/groups",
      "/staff/settings",
      "/planning",
      "/planning/requests",
      "/planning/intake",
      "/planning/unbooked",
      "/planning/wishes",
      "/planning/me",
      "/planning/settings",
      "/invoices",
      "/invoices/quick",
      "/invoices/journal",
      "/invoices/overdue",
      "/invoices/billing-runs",
      "/invoices/payments",
      "/invoices/settings",
      "/orders/quotes",
      "/orders/orders",
      "/orders/deliveries",
      "/orders/subscriptions",
      "/orders/analysis",
      "/orders/templates",
      "/orders/settings",
      "/communications/inbox",
      "/communications/channels",
      "/communications/suppressions",
      "/knowledge",
      "/knowledge/trash",
      "/agents",
      "/agents/review",
      "/agents/settings",
      "/products",
      "/products/categories",
      "/products/tax-categories",
      "/inventory",
      "/inventory/lots",
      "/inventory/expiring",
      "/inventory/transfers",
      "/inventory/movements",
      "/inventory/movements",
      "/inventory/counts",
      "/inventory/low-stock",
      "/inventory/import",
      "/manufacturing",
      "/manufacturing/orders",
      "/manufacturing/suggestions",
      "/manufacturing/unbuilds",
      "/manufacturing/shop-floor",
      "/manufacturing/schedule",
      "/manufacturing/deviations",
      "/manufacturing/boms",
      "/manufacturing/work-centers",
      "/energy/metering-points",
      "/energy/prices",
      "/energy/tariffs",
    ]);
  });

  // The calendar and My calendar (calendar feed design D9): the person's own
  // addresses and subscriptions, behind the one permission the app has.
  it("gives Calendar the calendar and My calendar, both behind calendar:access", () => {
    const calendar = appForKey("calendar");
    expect(calendar.requiredPermissions).toEqual(["calendar:access"]);
    const items = calendar.navSections.flatMap((section) => section.items);
    expect(items.map((item) => [item.label, item.to, item.requiredPermissions])).toEqual([
      ["navigation.calendar", "/calendar", ["calendar:access"]],
      ["navigation.myCalendar", "/calendar/settings", ["calendar:access"]],
    ]);
    expect(navigationCatalog.en["navigation.myCalendar"]).toBe("My calendar");
    expect(navigationCatalog.nb["navigation.myCalendar"]).toBe("Min kalender");
  });

  // The connection to the tenant (Entra design D15) is a page of Workspace admin, not an app
  // (Integrations design T2): the module has no tile, no sidebar of its own and no AppKey.
  it("gives Microsoft Entra ID no app: no tile, whoever asks", () => {
    expect(apps.some((app) => app.key === ("entra" as never) || app.module === "entra")).toBe(false);
    expect(() => appForKey("entra" as never)).toThrow(/entra/);
    const tiles = (permissions: string[]) => switcherTiles(permissions, allModules, "home").map((tile) => tile.app.key);
    expect(tiles(["entra:manage"])).not.toContain("entra");
    expect(tiles(["*"])).not.toContain("entra");
    expect(allNavSections.flatMap((section) => section.items).some((item) => item.to.startsWith("/entra"))).toBe(false);
    expect(navigationCatalog.en["navigation.entra"]).toBe("Microsoft Entra ID");
    expect(navigationCatalog.nb["navigation.entra"]).toBe("Microsoft Entra ID");
  });

  // The connection to the organisation (Google Workspace design G16) is a page of Workspace admin too.
  it("gives Google Workspace no app: no tile, whoever asks", () => {
    expect(apps.some((app) => app.key === ("google" as never) || app.module === "google")).toBe(false);
    expect(() => appForKey("google" as never)).toThrow(/google/);
    const tiles = (permissions: string[]) => switcherTiles(permissions, allModules, "home").map((tile) => tile.app.key);
    expect(tiles(["google:manage"])).not.toContain("google");
    expect(tiles(["*"])).not.toContain("google");
    expect(allNavSections.flatMap((section) => section.items).some((item) => item.to.startsWith("/google"))).toBe(
      false,
    );
  });

  // The Messenger connection (Messenger design N9) is a page of Workspace admin too, the Owner's.
  it("gives Meta no app: no tile, whoever asks", () => {
    expect(apps.some((app) => app.key === ("meta" as never) || app.module === "meta")).toBe(false);
    expect(() => appForKey("meta" as never)).toThrow(/meta/);
    const tiles = (permissions: string[]) => switcherTiles(permissions, allModules, "home").map((tile) => tile.app.key);
    expect(tiles(["*"])).not.toContain("meta");
    expect(navigationCatalog.en["navigation.meta"]).toBe("Meta");
    expect(navigationCatalog.nb["navigation.meta"]).toBe("Meta");
  });

  it("requires, for a tile, any permission that unlocks one of its sidebar entries", () => {
    expect(appForKey("customers").requiredPermissions).toEqual([
      "customers:view",
      "customers:contacts-view",
      "customers:associations-view",
      "customers:timeline-view",
    ]);
    expect(appForKey("home").requiredPermissions).toBeUndefined();
  });

  it("throws for an unknown key instead of returning undefined", () => {
    expect(() => appForKey("billing" as never)).toThrow(/billing/);
  });

  // The Projects app is three sidebar entries behind the same one permission,
  // so its tile is exactly as visible as they are: shown to a caller holding
  // projects:access, absent without it, and muted rather than hidden when the
  // installation did not mount the module. My tasks and the economy portfolio
  // come after the list and carry no search strategy of their own — the
  // portfolio's own validator supplies its defaults, and my tasks takes no URL
  // search params beyond the create intent Spotlight hands it. The template
  // catalog is a fourth entry, offered only to the callers who may change it
  // (projects:create with projects:access, what the API asks of every
  // template write); the extra permission is all-of, so the tile still
  // follows projects:access alone.
  it("gives Projects a list, My tasks and the economy portfolio behind projects:access, and a tile that follows them", () => {
    const projects = appForKey("projects");
    expect(projects).toMatchObject({ module: "projects", label: "navigation.projects", home: "/projects" });
    expect(projects.requiredPermissions).toEqual(["projects:access"]);
    const items = projects.navSections.flatMap((section) => section.items);
    expect(items.map((item) => [item.label, item.to, item.searchStrategy])).toEqual([
      ["navigation.projects", "/projects", "projects-list"],
      ["navigation.myTasks", "/projects/my-tasks", undefined],
      ["navigation.projectsEconomy", "/projects/economy", undefined],
      ["navigation.projectTemplates", "/projects/templates", undefined],
    ]);
    for (const item of items) expect(item.requiredPermissions).toEqual(["projects:access"]);
    expect(items.map((item) => item.requiredAllPermissions)).toEqual([
      undefined,
      undefined,
      undefined,
      ["projects:create"],
    ]);
  });

  it("offers Project templates in the sidebar only with projects:create and projects:access", () => {
    const projectsItems = (permissions: string[]) =>
      visibleNavSections(appForKey("projects").navSections, {
        permissions,
        isOwner: false,
        canManageAuthorization: false,
        enabledModules: allModules,
      })
        .flatMap((section) => section.items)
        .map((item) => item.to);
    expect(projectsItems(["projects:access"])).not.toContain("/projects/templates");
    expect(projectsItems(["projects:create"])).not.toContain("/projects/templates");
    expect(projectsItems(["projects:access", "projects:create"])).toContain("/projects/templates");
  });

  it("shows the Projects tile for projects:access, hides it without, and mutes it when the module is off", () => {
    const keys = (permissions: string[], enabled: readonly (typeof allModules)[number][]) =>
      switcherTiles(permissions, enabled, undefined).map((tile) => [tile.app.key, tile.enabled]);
    expect(keys(["projects:access"], allModules)).toContainEqual(["projects", true]);
    expect(keys(["customers:view"], allModules).map(([key]) => key)).not.toContain("projects");
    expect(keys(["projects:access"], ["customers"])).toContainEqual(["projects", false]);
  });

  // Time is four destinations behind four different permissions, so the tile
  // is as visible as the least of them: anyone who may log an hour gets it,
  // and the sidebar then shows only the pages that caller may open. Approvals
  // is the interesting one — the sidebar entry is for the dedicated approvers,
  // while a project manager reaches the same queue from the dashboard (see the
  // guard's rule below).
  it("gives Time four destinations, each behind its own permission", () => {
    const time = appForKey("time");
    expect(time).toMatchObject({ module: "time", label: "navigation.time", home: "/time" });
    expect(time.requiredPermissions).toEqual(["time:access", "time:approve", "time:view-all", "time:manage"]);
    const items = time.navSections.flatMap((section) => section.items);
    expect(items.map((item) => [item.label, item.to, item.requiredPermissions, item.searchStrategy])).toEqual([
      ["navigation.myWeek", "/time", ["time:access"], "time-week"],
      ["navigation.approvals", "/time/approvals", ["time:approve"], undefined],
      ["navigation.people", "/time/people", ["time:view-all"], undefined],
      ["navigation.timeSettings", "/time/settings", ["time:manage"], undefined],
    ]);
  });

  it("shows the Time tile for time:access, hides it without, and mutes it when the module is off", () => {
    const keys = (permissions: string[], enabled: readonly (typeof allModules)[number][]) =>
      switcherTiles(permissions, enabled, undefined).map((tile) => [tile.app.key, tile.enabled]);
    expect(keys(["time:access"], allModules)).toContainEqual(["time", true]);
    expect(keys(["customers:view"], allModules).map(([key]) => key)).not.toContain("time");
    expect(keys(["time:access"], ["customers"])).toContainEqual(["time", false]);
  });

  it("shows a time:access holder My week alone, and each further page with its permission", () => {
    const paths = (permissions: string[]) =>
      appNavSections(appForKey("time"), allModules, {
        permissions,
        isOwner: false,
        canManageAuthorization: false,
        enabledModules: allModules,
      }).flatMap((section) => section.items.map((item) => item.to));
    expect(paths(["time:access"])).toEqual(["/time"]);
    expect(paths(["time:access", "time:approve"])).toEqual(["/time", "/time/approvals"]);
    expect(paths(["time:access", "time:view-all"])).toEqual(["/time", "/time/people"]);
    expect(paths(["time:access", "time:manage"])).toEqual(["/time", "/time/settings"]);
    expect(paths(["*"])).toEqual(["/time", "/time/approvals", "/time/people", "/time/settings"]);
  });

  // Expenses is four destinations behind three different permissions, the
  // same shape as Time: Approvals is offered to the dedicated approvers in
  // the sidebar, but a project manager approves their own project's expenses
  // through their role alone and reaches the same queue from the dashboard's
  // attention list — the guard only asks for the app (see the module access
  // guard's own tests).
  it("gives Expenses four destinations, each behind its own permission", () => {
    const expenses = appForKey("expenses");
    expect(expenses).toMatchObject({ module: "expenses", label: "navigation.expenses", home: "/expenses" });
    expect(expenses.requiredPermissions).toEqual(["expenses:access", "expenses:approve", "expenses:manage"]);
    const items = expenses.navSections.flatMap((section) => section.items);
    expect(items.map((item) => [item.label, item.to, item.requiredPermissions, item.guardPermissions])).toEqual([
      ["navigation.myExpenses", "/expenses", ["expenses:access"], undefined],
      ["navigation.expenseApprovals", "/expenses/approvals", ["expenses:approve"], ["expenses:access"]],
      ["navigation.reimbursements", "/expenses/reimbursements", ["expenses:manage"], undefined],
      ["navigation.expensesSettings", "/expenses/settings", ["expenses:manage"], undefined],
    ]);
  });

  // Invoices is six destinations: the list, the journal and Overdue for
  // anyone who reads invoices, the quick invoice for whoever may both create
  // and issue (invoices payments and reminders design D17, D22), Payments for
  // invoices:payments (D22), the settings for invoices:manage (invoices
  // foundation design D12).
  it("gives Invoices the list, the quick invoice, the journal, Overdue, Billing runs, Payments and the settings", () => {
    const invoices = appForKey("invoices");
    expect(invoices).toMatchObject({ module: "invoices", label: "navigation.invoices", home: "/invoices" });
    expect(invoices.requiredPermissions).toEqual(["invoices:access", "invoices:payments", "invoices:manage"]);
    const items = invoices.navSections.flatMap((section) => section.items);
    expect(items.map((item) => [item.label, item.to, item.requiredPermissions])).toEqual([
      ["navigation.invoices", "/invoices", ["invoices:access"]],
      ["navigation.invoiceQuick", "/invoices/quick", ["invoices:access"]],
      ["navigation.invoiceJournal", "/invoices/journal", ["invoices:access"]],
      ["navigation.invoiceOverdue", "/invoices/overdue", ["invoices:access"]],
      ["navigation.invoiceBillingRuns", "/invoices/billing-runs", ["invoices:access"]],
      ["navigation.invoicePayments", "/invoices/payments", ["invoices:payments"]],
      ["navigation.invoiceSettings", "/invoices/settings", ["invoices:manage"]],
    ]);
  });

  // hasPermissions is any-of: the Payments entry names invoices:payments
  // alone, so a reader who holds invoices:access only never sees an entry
  // whose every request would be a 403.
  it("Nav_PaymentsBehindItsPermission", () => {
    const paths = (permissions: string[]) =>
      appNavSections(appForKey("invoices"), allModules, {
        permissions,
        isOwner: false,
        canManageAuthorization: false,
        enabledModules: allModules,
      }).flatMap((section) => section.items.map((item) => item.to));
    expect(paths(["invoices:access"])).toEqual(["/invoices", "/invoices/journal", "/invoices/overdue"]);
    expect(paths(["invoices:access", "invoices:payments"])).toEqual([
      "/invoices",
      "/invoices/journal",
      "/invoices/overdue",
      "/invoices/payments",
    ]);
    expect(paths(["invoices:access", "invoices:manage"])).not.toContain("/invoices/payments");
  });

  // The quick invoice's API asks for invoices:access, invoices:create and
  // invoices:issue together (D17), meta's canQuickInvoice. hasPermissions is
  // any-of, so the entry names the app's own permission as requiredPermissions
  // and the other two as requiredAllPermissions: a drafter or an issuer alone
  // never sees an entry whose page would only say "not allowed".
  it("Nav_QuickInvoiceBehindIssue", () => {
    const paths = (permissions: string[]) =>
      appNavSections(appForKey("invoices"), allModules, {
        permissions,
        isOwner: false,
        canManageAuthorization: false,
        enabledModules: allModules,
      }).flatMap((section) => section.items.map((item) => item.to));
    const quick = appForKey("invoices")
      .navSections.flatMap((section) => section.items)
      .find((item) => item.to === "/invoices/quick");
    expect(quick?.requiredAllPermissions).toEqual(["invoices:create", "invoices:issue"]);
    expect(paths(["invoices:access", "invoices:create", "invoices:issue"])).toContain("/invoices/quick");
    expect(paths(["*"])).toContain("/invoices/quick");
    expect(paths(["invoices:access", "invoices:issue"])).not.toContain("/invoices/quick");
    expect(paths(["invoices:access", "invoices:create"])).not.toContain("/invoices/quick");
    expect(paths(["invoices:create", "invoices:issue"])).not.toContain("/invoices/quick");
  });

  // Billing runs (energy consumption billing design D10, D11): making and
  // cancelling a run ask for invoices:access and invoices:create together, so
  // a reader alone is never offered the entry.
  it("Nav_BillingRunsBehindCreate", () => {
    const paths = (permissions: string[]) =>
      appNavSections(appForKey("invoices"), allModules, {
        permissions,
        isOwner: false,
        canManageAuthorization: false,
        enabledModules: allModules,
      }).flatMap((section) => section.items.map((item) => item.to));
    expect(paths(["invoices:access", "invoices:create"])).toContain("/invoices/billing-runs");
    expect(paths(["*"])).toContain("/invoices/billing-runs");
    expect(paths(["invoices:access"])).not.toContain("/invoices/billing-runs");
    expect(paths(["invoices:create"])).not.toContain("/invoices/billing-runs");
    expect(navigationCatalog.en["navigation.invoiceBillingRuns"]).toBe("Billing runs");
    expect(navigationCatalog.nb["navigation.invoiceBillingRuns"]).toBe("Fakturakjøringer");
  });

  it("names the quick invoice in both languages", () => {
    expect(navigationCatalog.en["navigation.invoiceQuick"]).toBe("Quick invoice");
    expect(navigationCatalog.nb["navigation.invoiceQuick"]).toBe("Hurtigfaktura");
  });

  // Overdue is read by everyone who reads invoices (D12): its entry names
  // invoices:access alone; sending reminders and paper letters inside it are
  // the page's own, behind meta's canRunReminders.
  it("Nav_OverdueBehindAccess", () => {
    const paths = (permissions: string[]) =>
      appNavSections(appForKey("invoices"), allModules, {
        permissions,
        isOwner: false,
        canManageAuthorization: false,
        enabledModules: allModules,
      }).flatMap((section) => section.items.map((item) => item.to));
    expect(paths(["invoices:access"])).toContain("/invoices/overdue");
    expect(paths(["invoices:payments"])).not.toContain("/invoices/overdue");
    expect(paths(["invoices:manage"])).not.toContain("/invoices/overdue");
  });

  it("shows the Expenses tile for expenses:access, hides it without, and mutes it when the module is off", () => {
    const keys = (permissions: string[], enabled: readonly (typeof allModules)[number][]) =>
      switcherTiles(permissions, enabled, undefined).map((tile) => [tile.app.key, tile.enabled]);
    expect(keys(["expenses:access"], allModules)).toContainEqual(["expenses", true]);
    expect(keys(["customers:view"], allModules).map(([key]) => key)).not.toContain("expenses");
    expect(keys(["expenses:access"], ["customers"])).toContainEqual(["expenses", false]);
  });

  it("shows an expenses:access holder My expenses alone, and each further page with its permission", () => {
    const paths = (permissions: string[]) =>
      appNavSections(appForKey("expenses"), allModules, {
        permissions,
        isOwner: false,
        canManageAuthorization: false,
        enabledModules: allModules,
      }).flatMap((section) => section.items.map((item) => item.to));
    expect(paths(["expenses:access"])).toEqual(["/expenses"]);
    expect(paths(["expenses:access", "expenses:approve"])).toEqual(["/expenses", "/expenses/approvals"]);
    expect(paths(["expenses:access", "expenses:manage"])).toEqual([
      "/expenses",
      "/expenses/reimbursements",
      "/expenses/settings",
    ]);
    expect(paths(["*"])).toEqual([
      "/expenses",
      "/expenses/approvals",
      "/expenses/reimbursements",
      "/expenses/settings",
    ]);
  });

  // Staff's destinations are the package's own (`staffNavItems`): where each
  // is, which permission offers it and which the guard asks for are the
  // module's to say, so the host adds only the label and the icon. People is
  // offered to the directory (`staff:view`) and to HR without it
  // (`staff:manage`), and guarded by the app alone — the server judges the
  // rest. Its label is not Time's bare "People": Spotlight flattens every
  // app's destinations into one list, as Expense approvals' comment says.
  it("gives Staff My page, the staff directory and the settings, from the package's destinations", () => {
    const staff = appForKey("staff");
    expect(staff).toMatchObject({ module: "staff", label: "navigation.staff", home: "/staff" });
    expect(staff.icon).toBe(IconUsersGroup);
    expect(
      staff.navSections.flatMap((section) => section.items).find((item) => item.to === "/staff/absence")?.icon,
    ).toBe(IconCalendarEvent);
    // The tile asks for the app itself: the server refuses every Staff call
    // without staff:access, whatever else the caller holds.
    expect(staff.requiredPermissions).toEqual(["staff:access"]);
    const items = staff.navSections.flatMap((section) => section.items);
    expect(items.map((item) => [item.label, item.to, item.requiredPermissions, item.guardPermissions])).toEqual([
      ["navigation.staffMyPage", "/staff/me", ["staff:access"], undefined],
      ["navigation.staffPeople", "/staff/people", ["staff:view", "staff:manage"], ["staff:access"]],
      [
        "navigation.staffAbsence",
        "/staff/absence",
        ["staff:view", "staff:approve", "staff:manage", "staff:personal-data"],
        ["staff:access"],
      ],
      ["navigation.staffCompetence", "/staff/competence", ["staff:view", "staff:manage"], undefined],
      ["navigation.staffGroups", "/staff/groups", ["staff:manage"], undefined],
      ["navigation.staffSettings", "/staff/settings", ["staff:manage"], undefined],
    ]);
    expect(items.map((item) => item.to)).toEqual(staffNavItems.map((item) => item.to));
  });

  it("shows the Staff tile for staff:access, hides it without, and mutes it when the module is off", () => {
    const keys = (permissions: string[], enabled: readonly (typeof allModules)[number][]) =>
      switcherTiles(permissions, enabled, undefined).map((tile) => [tile.app.key, tile.enabled]);
    expect(keys(["staff:access"], allModules)).toContainEqual(["staff", true]);
    expect(keys(["customers:view", "expenses:access"], allModules).map(([key]) => key)).not.toContain("staff");
    expect(keys(["staff:access"], ["customers"])).toContainEqual(["staff", false]);
    expect(keys(["staff:view", "staff:manage"], allModules).map(([key]) => key)).not.toContain("staff");
  });

  it("shows a staff:access holder My page alone, and the directory and settings with their permissions", () => {
    const paths = (permissions: string[]) =>
      appNavSections(appForKey("staff"), allModules, {
        permissions,
        isOwner: false,
        canManageAuthorization: false,
        enabledModules: allModules,
      }).flatMap((section) => section.items.map((item) => item.to));
    expect(paths(["staff:access"])).toEqual(["/staff/me"]);
    // Absence for whoever reads the calendar or decides: the directory, the
    // Approver, HR, and staff:personal-data, which decides the HR-only types.
    expect(paths(["staff:access", "staff:approve"])).toEqual(["/staff/me", "/staff/absence"]);
    expect(paths(["staff:access", "staff:personal-data"])).toEqual(["/staff/me", "/staff/absence"]);
    // The Competence area for the directory and HR (staff competence design S14).
    expect(paths(["staff:access", "staff:view"])).toEqual([
      "/staff/me",
      "/staff/people",
      "/staff/absence",
      "/staff/competence",
    ]);
    expect(paths(["staff:access", "staff:manage"])).toEqual([
      "/staff/me",
      "/staff/people",
      "/staff/absence",
      "/staff/competence",
      "/staff/groups",
      "/staff/settings",
    ]);
    expect(paths(["*"])).toEqual([
      "/staff/me",
      "/staff/people",
      "/staff/absence",
      "/staff/competence",
      "/staff/groups",
      "/staff/settings",
    ]);
  });

  // Planning's destinations are the package's (`planningNavItems`), the host
  // adding the label and the icon. The board is offered to planning:view but
  // guarded by the app alone: its URL is the prefix a person's own timeline
  // lives under, and the board's route sends a caller without planning:view
  // to My planning.
  it("gives Planning the board, Requests, Intake, Unbooked, Wishes, My planning and the settings, from the package's destinations", () => {
    const planning = appForKey("planning");
    expect(planning).toMatchObject({ module: "planning", label: "navigation.planning", home: "/planning" });
    expect(planning.icon).toBe(IconCalendarStats);
    expect(planning.requiredPermissions).toEqual(["planning:access"]);
    const items = planning.navSections.flatMap((section) => section.items);
    expect(items.map((item) => [item.label, item.to, item.requiredPermissions, item.guardPermissions])).toEqual([
      ["navigation.planningBoard", "/planning", ["planning:view"], ["planning:access"]],
      ["navigation.planningRequests", "/planning/requests", ["planning:view"], undefined],
      ["navigation.planningIntake", "/planning/intake", ["planning:view"], undefined],
      ["navigation.planningUnbooked", "/planning/unbooked", ["planning:view"], undefined],
      ["navigation.planningWishes", "/planning/wishes", ["planning:manage"], undefined],
      ["navigation.planningMyPlanning", "/planning/me", ["planning:access"], undefined],
      ["navigation.planningSettings", "/planning/settings", ["planning:manage"], undefined],
    ]);
    expect(items.map((item) => item.to)).toEqual(planningNavItems.map((item) => item.to));
    for (const item of items) {
      expect(navigationCatalog.en[item.label as keyof typeof navigationCatalog.en]).toBeTruthy();
      expect(navigationCatalog.nb[item.label as keyof typeof navigationCatalog.nb]).toBeTruthy();
    }
    expect(navigationCatalog.en["navigation.planning"]).toBe("Planning");
    expect(navigationCatalog.nb["navigation.planning"]).toBe("Bemanning");
  });

  it("shows the Planning tile for planning:access, and a holder of it alone My planning alone", () => {
    const keys = (permissions: string[], enabled: readonly (typeof allModules)[number][]) =>
      switcherTiles(permissions, enabled, undefined).map((tile) => [tile.app.key, tile.enabled]);
    expect(keys(["planning:access"], allModules)).toContainEqual(["planning", true]);
    expect(keys(["planning:view", "planning:manage"], allModules).map(([key]) => key)).not.toContain("planning");
    expect(keys(["planning:access"], ["customers"])).toContainEqual(["planning", false]);
    const paths = (permissions: string[]) =>
      appNavSections(appForKey("planning"), allModules, {
        permissions,
        isOwner: false,
        canManageAuthorization: false,
        enabledModules: allModules,
      }).flatMap((section) => section.items.map((item) => item.to));
    expect(paths(["planning:access"])).toEqual(["/planning/me"]);
    expect(paths(["planning:access", "planning:view"])).toEqual([
      "/planning",
      "/planning/requests",
      "/planning/intake",
      "/planning/unbooked",
      "/planning/me",
    ]);
    expect(paths(["*"])).toEqual([
      "/planning",
      "/planning/requests",
      "/planning/intake",
      "/planning/unbooked",
      "/planning/wishes",
      "/planning/me",
      "/planning/settings",
    ]);
  });

  // Inventory's destinations are the package's (`inventoryNavItems`), the host
  // adding the label and the icon. Receipts is the movements list filtered to
  // receipts (inventory design §13.3), so it shares Movements' path and
  // carries the search. Transfers needs both views alone (phase 2 design
  // I63). Counts and Import are offered by counts-manage and stock-adjust,
  // each with both views, as every stock read asks (§5).
  it("gives Inventory the overview, Lots, Expiring, Transfers, Movements, Receipts, Counts and Import, from the package's destinations", () => {
    const inventory = appForKey("inventory");
    expect(inventory).toMatchObject({ module: "inventory", label: "navigation.inventory", home: "/inventory" });
    expect(inventory.icon).toBe(IconBuildingWarehouse);
    expect(inventory.requiredPermissions).toEqual(["inventory:locations-view", "inventory:stock-view"]);
    const items = inventory.navSections.flatMap((section) => section.items);
    expect(
      items.map((item) => [
        item.label,
        item.to,
        item.search,
        item.requiredPermissions,
        item.requiredAllPermissions,
        item.guardPermissions,
      ]),
    ).toEqual([
      [
        "navigation.inventoryOverview",
        "/inventory",
        undefined,
        ["inventory:locations-view", "inventory:stock-view"],
        undefined,
        undefined,
      ],
      [
        "navigation.inventoryLots",
        "/inventory/lots",
        undefined,
        ["inventory:stock-view"],
        ["inventory:locations-view", "inventory:stock-view"],
        undefined,
      ],
      [
        "navigation.inventoryExpiring",
        "/inventory/expiring",
        undefined,
        ["inventory:stock-view"],
        ["inventory:locations-view", "inventory:stock-view"],
        undefined,
      ],
      [
        "navigation.inventoryTransfers",
        "/inventory/transfers",
        undefined,
        ["inventory:stock-view"],
        ["inventory:locations-view", "inventory:stock-view"],
        undefined,
      ],
      [
        "navigation.inventoryMovements",
        "/inventory/movements",
        undefined,
        ["inventory:stock-view"],
        ["inventory:locations-view", "inventory:stock-view"],
        undefined,
      ],
      [
        "navigation.inventoryReceipts",
        "/inventory/movements",
        { kind: "receipt" },
        ["inventory:stock-view"],
        ["inventory:locations-view", "inventory:stock-view"],
        undefined,
      ],
      [
        "navigation.inventoryCounts",
        "/inventory/counts",
        undefined,
        ["inventory:counts-manage"],
        ["inventory:locations-view", "inventory:stock-view"],
        undefined,
      ],
      [
        "navigation.inventoryLowStock",
        "/inventory/low-stock",
        undefined,
        ["inventory:stock-view"],
        ["inventory:locations-view", "inventory:stock-view"],
        undefined,
      ],
      [
        "navigation.inventoryImport",
        "/inventory/import",
        undefined,
        ["inventory:stock-adjust"],
        ["inventory:locations-view", "inventory:stock-view"],
        undefined,
      ],
    ]);
    expect(items.map((item) => item.icon)).toEqual([
      IconBuildingWarehouse,
      IconBarcode,
      IconCalendarDue,
      IconTruckDelivery,
      IconTransfer,
      IconPackageImport,
      IconClipboardCheck,
      IconTrendingDown,
      IconFileImport,
    ]);
    expect(items.map((item) => item.to)).toEqual(inventoryNavItems.map((item) => item.to));
    for (const item of items) {
      expect(navigationCatalog.en[item.label as keyof typeof navigationCatalog.en]).toBeTruthy();
      expect(navigationCatalog.nb[item.label as keyof typeof navigationCatalog.nb]).toBeTruthy();
    }
    expect(navigationCatalog.en["navigation.inventory"]).toBe("Inventory");
    expect(navigationCatalog.nb["navigation.inventory"]).toBe("Lager");
  });

  it("shows the Inventory tile for either view permission, and locations-view alone the overview alone", () => {
    const keys = (permissions: string[], enabled: readonly (typeof allModules)[number][]) =>
      switcherTiles(permissions, enabled, undefined).map((tile) => [tile.app.key, tile.enabled]);
    expect(keys(["inventory:locations-view"], allModules)).toContainEqual(["inventory", true]);
    expect(keys(["inventory:stock-view"], allModules)).toContainEqual(["inventory", true]);
    // Counts' and Import's own permissions need both views beside them, so alone they offer nothing.
    expect(keys(["inventory:stock-adjust"], allModules).map(([key]) => key)).not.toContain("inventory");
    expect(keys(["inventory:counts-manage"], allModules).map(([key]) => key)).not.toContain("inventory");
    expect(keys(["inventory:locations-view"], ["customers"])).toContainEqual(["inventory", false]);
    const paths = (permissions: string[]) =>
      appNavSections(appForKey("inventory"), allModules, {
        permissions,
        isOwner: false,
        canManageAuthorization: false,
        enabledModules: allModules,
      }).flatMap((section) => section.items.map((item) => item.label));
    expect(paths(["inventory:locations-view"])).toEqual(["navigation.inventoryOverview"]);
    expect(paths(["inventory:locations-view", "inventory:stock-view"])).toEqual([
      "navigation.inventoryOverview",
      "navigation.inventoryLots",
      "navigation.inventoryExpiring",
      "navigation.inventoryTransfers",
      "navigation.inventoryMovements",
      "navigation.inventoryReceipts",
      "navigation.inventoryLowStock",
    ]);
    expect(
      paths(["inventory:locations-view", "inventory:stock-view", "inventory:counts-manage", "inventory:stock-adjust"]),
    ).toEqual([
      "navigation.inventoryOverview",
      "navigation.inventoryLots",
      "navigation.inventoryExpiring",
      "navigation.inventoryTransfers",
      "navigation.inventoryMovements",
      "navigation.inventoryReceipts",
      "navigation.inventoryCounts",
      "navigation.inventoryLowStock",
      "navigation.inventoryImport",
    ]);
    expect(paths(["inventory:locations-view", "inventory:counts-manage"])).toEqual(["navigation.inventoryOverview"]);
  });

  // Manufacturing's destinations are the package's (`manufacturingNavItems`,
  // M7): Overview, Manufacturing orders, Shop floor, Production schedule,
  // Bills of materials and Work centers, each offered by manufacturing:access;
  // the labels and icons are the host's.
  it("registers Manufacturing with the package's nine destinations, all manufacturing:access's", () => {
    const manufacturing = appForKey("manufacturing");
    expect(manufacturing).toMatchObject({
      module: "manufacturing",
      label: "navigation.manufacturing",
      home: "/manufacturing",
    });
    expect(manufacturing.icon).toBe(IconBuildingFactory2);
    expect(manufacturing.requiredPermissions).toEqual(["manufacturing:access"]);
    const items = manufacturing.navSections.flatMap((section) => section.items);
    expect(items.map((item) => [item.label, item.to, item.requiredPermissions])).toEqual([
      ["navigation.manufacturingOverview", "/manufacturing", ["manufacturing:access"]],
      ["navigation.manufacturingOrders", "/manufacturing/orders", ["manufacturing:access"]],
      ["navigation.manufacturingSuggestions", "/manufacturing/suggestions", ["manufacturing:access"]],
      ["navigation.manufacturingUnbuilds", "/manufacturing/unbuilds", ["manufacturing:access"]],
      ["navigation.manufacturingShopFloor", "/manufacturing/shop-floor", ["manufacturing:access"]],
      ["navigation.manufacturingSchedule", "/manufacturing/schedule", ["manufacturing:access"]],
      ["navigation.manufacturingDeviations", "/manufacturing/deviations", ["manufacturing:access"]],
      ["navigation.manufacturingBoms", "/manufacturing/boms", ["manufacturing:access"]],
      ["navigation.manufacturingWorkCenters", "/manufacturing/work-centers", ["manufacturing:access"]],
    ]);
    expect(items.map((item) => item.icon)).toEqual([
      IconBuildingFactory2,
      IconClipboardList,
      IconBulb,
      IconPuzzle,
      IconHammer,
      IconCalendarTime,
      IconZoomExclamation,
      IconListDetails,
      IconTool,
    ]);
    expect(items.map((item) => item.to)).toEqual(manufacturingNavItems.map((item) => item.to));
    expect(navigationCatalog.en["navigation.manufacturing"]).toBe("Manufacturing");
    expect(navigationCatalog.nb["navigation.manufacturing"]).toBe("Produksjon");
    expect(
      [
        "navigation.manufacturingOverview",
        "navigation.manufacturingOrders",
        "navigation.manufacturingShopFloor",
        "navigation.manufacturingSchedule",
        "navigation.manufacturingBoms",
        "navigation.manufacturingWorkCenters",
      ].map((key) => navigationCatalog.nb[key as keyof typeof navigationCatalog.nb]),
    ).toEqual(["Oversikt", "Produksjonsordrer", "Verksted", "Produksjonsplan", "Stykklister", "Arbeidsstasjoner"]);
    const keys = (permissions: string[], enabled: readonly ModuleKey[]) =>
      switcherTiles(permissions, enabled, undefined).map((tile) => [tile.app.key, tile.enabled]);
    expect(keys(["manufacturing:access"], ["manufacturing"])).toContainEqual(["manufacturing", true]);
    expect(keys(["manufacturing:access"], ["customers"])).toContainEqual(["manufacturing", false]);
    expect(keys(["manufacturing:boms-manage"], ["manufacturing"]).map(([key]) => key)).not.toContain("manufacturing");
  });

  it("treats Home as always enabled and module apps as enabled when their module is", () => {
    expect(isAppEnabled(appForKey("home"), [])).toBe(true);
    expect(isAppEnabled(appForKey("energy"), ["customers"])).toBe(false);
    expect(isAppEnabled(appForKey("energy"), ["customers", "energy"])).toBe(true);
  });
});

describe("the administration areas", () => {
  const visibility: NavVisibilityContext = {
    permissions: [],
    isOwner: false,
    canManageAuthorization: false,
    enabledModules: allModules,
  };

  it("declares settings, workspace and admin, none of them a switcher tile", () => {
    expect(areas.map((area) => area.key)).toEqual(["settings", "workspace", "admin"]);
    const tiles = switcherTiles(["*"], allModules, "settings");
    expect(tiles.map((tile) => tile.app.key)).toEqual([
      "home",
      "calendar",
      "customers",
      "projects",
      "time",
      "expenses",
      "staff",
      "planning",
      "invoices",
      "orders",
      "communications",
      "knowledge",
      "agents",
      "products",
      "inventory",
      "manufacturing",
      "energy",
    ]);
    expect(tiles.some((tile) => tile.current)).toBe(false);
  });

  it("keeps every area destination under the area's URL prefix", () => {
    for (const area of areas) {
      const prefix = `/${area.key}`;
      for (const item of area.navSections.flatMap((section) => section.items)) {
        expect(item.to === prefix || item.to.startsWith(`${prefix}/`)).toBe(true);
        // An area belongs to no module; only an entry a module provides, Entra's, Google's and Orders'
        // Shipping Integrations pages, the Assistant's and Manufacturing's Stations, names its module.
        // Connections names the providers' modules in `modules`.
        const owner: Record<string, string> = {
          "/workspace/entra": "entra",
          "/workspace/google": "google",
          "/workspace/meta": "meta",
          "/workspace/shipping": "orders",
          "/workspace/assistant": "assistant",
          "/workspace/stations": "manufacturing",
        };
        expect(item.module).toBe(owner[item.to]);
      }
    }
  });

  it("resolves an area or an app by key, and throws for anything else", () => {
    expect(areaForKey("workspace")).toBe(areas[1]);
    expect(areaForKey("customers")).toBe(appForKey("customers"));
    expect(() => areaForKey("billing" as never)).toThrow(/billing/);
  });

  it("labels another module's name as its app, and leaves a name no app has", () => {
    expect(moduleLabelKey("manufacturing")).toBe(appForKey("manufacturing").label);
    expect(moduleLabelKey("orders")).toBe("navigation.orders");
    expect(moduleLabelKey("workspace")).toBeUndefined();
    expect(moduleLabelKey("billing")).toBeUndefined();
  });

  it("is always enabled: an area has no module to turn off", () => {
    expect(isAppEnabled(areaForKey("settings"), [])).toBe(true);
  });

  const workspacePaths = (sections: ReturnType<typeof appNavSections>) =>
    sections.flatMap((section) => section.items.map((item) => item.to));
  const ownerOnWorkspace: NavVisibilityContext = { ...visibility, isOwner: true, permissions: ["*"] };
  const workspaceFor = (context: NavVisibilityContext, modules: readonly (typeof allModules)[number][] = allModules) =>
    appNavSections(areaForKey("workspace"), modules, { ...context, enabledModules: modules });

  it("shows the whole workspace area to an Owner, then Integrations", () => {
    expect(workspacePaths(workspaceFor(ownerOnWorkspace))).toEqual([
      "/workspace/overview",
      "/workspace/users",
      "/workspace/invitations",
      "/workspace/entra",
      "/workspace/google",
    ]);
    expect(workspaceFor(ownerOnWorkspace).map((section) => section.label)).toEqual([
      undefined,
      "navigation.integrations",
    ]);
  });

  it("shows an Entra manager who is not an Owner the Integrations section alone", () => {
    const manager = { ...visibility, permissions: ["entra:manage"] };
    const sections = workspaceFor(manager);
    expect(workspacePaths(sections)).toEqual(["/workspace/entra"]);
    expect(sections.map((section) => section.label)).toEqual(["navigation.integrations"]);
  });

  it("shows an authorization manager roles alone, and nothing of Integrations without entra:manage", () => {
    expect(workspacePaths(workspaceFor({ ...visibility, canManageAuthorization: true }))).toEqual(["/workspace/roles"]);
    expect(workspacePaths(workspaceFor(visibility))).toEqual([]);
  });

  it("shows an authorization manager who also manages Entra both sections", () => {
    const both = { ...visibility, canManageAuthorization: true, permissions: ["entra:manage"] };
    expect(workspacePaths(workspaceFor(both))).toEqual(["/workspace/roles", "/workspace/entra"]);
  });

  it("hides Integrations, section and all, while the Entra and Google modules are off", () => {
    const withoutEither = allModules.filter((key) => key !== "entra" && key !== "google");
    const sections = workspaceFor(ownerOnWorkspace, withoutEither);
    expect(workspacePaths(sections)).toEqual(["/workspace/overview", "/workspace/users", "/workspace/invitations"]);
    expect(sections.map((section) => section.label)).toEqual([undefined]);
    expect(workspacePaths(workspaceFor({ ...visibility, permissions: ["entra:manage"] }, withoutEither))).toEqual([]);
    expect(workspacePaths(workspaceFor({ ...visibility, permissions: ["google:manage"] }, withoutEither))).toEqual([]);
  });

  it("shows a Google manager who is not an Owner Google Workspace alone, while the module is on", () => {
    const manager = { ...visibility, permissions: ["google:manage"] };
    const sections = workspaceFor(manager);
    expect(workspacePaths(sections)).toEqual(["/workspace/google"]);
    expect(sections.map((section) => section.label)).toEqual(["navigation.integrations"]);
    const withoutGoogle = allModules.filter((key) => key !== "google");
    expect(workspacePaths(workspaceFor(manager, withoutGoogle))).toEqual([]);
    expect(workspacePaths(workspaceFor(ownerOnWorkspace, withoutGoogle))).not.toContain("/workspace/google");
  });

  it("gates the Integrations item on the module and its permission, as Connections gates on the module", () => {
    const integrations = areaForKey("workspace").navSections.find(
      (section) => section.label === "navigation.integrations",
    );
    expect(integrations?.items).toHaveLength(6);
    expect(integrations?.items[0]).toMatchObject({
      label: "navigation.entra",
      to: "/workspace/entra",
      module: "entra",
      requiredPermissions: ["entra:manage"],
    });
    expect(integrations?.items[1]).toMatchObject({
      label: "navigation.google",
      to: "/workspace/google",
      module: "google",
      requiredPermissions: ["google:manage"],
    });
    // Meta (Messenger design N9), the Owner's.
    expect(integrations?.items[2]).toMatchObject({
      label: "navigation.meta",
      to: "/workspace/meta",
      module: "meta",
      ownerOnly: true,
    });
    // Orders' carrier accounts (orders design O51), behind orders:carriers-manage.
    expect(integrations?.items[3]).toMatchObject({
      label: "navigation.shipping",
      to: "/workspace/shipping",
      module: "orders",
      requiredPermissions: ["orders:carriers-manage"],
    });
    expect(navigationCatalog.nb["navigation.shipping"]).toBe("Frakt");
    expect(navigationCatalog.en["navigation.google"]).toBe("Google Workspace");
    expect(navigationCatalog.nb["navigation.google"]).toBe("Google Workspace");
    expect(navigationCatalog.en["navigation.integrations"]).toBe("Integrations");
    expect(navigationCatalog.nb["navigation.integrations"]).toBe("Integrasjoner");
  });

  // AI models (Integrations design T13): the Owner's, and there only while the instance has an AI
  // provider, which the root layout learns from the settings endpoint and passes as a feature.
  describe("the AI models item", () => {
    const withAi: NavVisibilityContext = { ...ownerOnWorkspace, features: ["ai"] };

    it("is the Owner's second Integrations item, present while the instance has an AI provider", () => {
      expect(workspacePaths(workspaceFor(withAi))).toEqual([
        "/workspace/overview",
        "/workspace/users",
        "/workspace/invitations",
        "/workspace/entra",
        "/workspace/google",
        "/workspace/ai",
        "/workspace/assistant",
      ]);
      const item = areaForKey("workspace")
        .navSections.flatMap((section) => section.items)
        .find((one) => one.to === "/workspace/ai");
      expect(item).toMatchObject({ label: "navigation.aiModels", ownerOnly: true, feature: "ai" });
      expect(item?.module).toBeUndefined();
      expect(navigationCatalog.en["navigation.aiModels"]).toBe("AI models");
      expect(navigationCatalog.nb["navigation.aiModels"]).toBe("KI-modeller");
    });

    it("is absent without an AI provider, whether the features are unknown or empty", () => {
      expect(workspacePaths(workspaceFor(ownerOnWorkspace))).not.toContain("/workspace/ai");
      expect(workspacePaths(workspaceFor({ ...ownerOnWorkspace, features: [] }))).not.toContain("/workspace/ai");
    });

    it("is not offered to anyone but an Owner, the Entra manager included", () => {
      const manager = { ...visibility, permissions: ["entra:manage"], features: ["ai" as const] };
      expect(workspacePaths(workspaceFor(manager))).toEqual(["/workspace/entra"]);
      expect(workspacePaths(workspaceFor({ ...manager, canManageAuthorization: true }))).toEqual([
        "/workspace/roles",
        "/workspace/entra",
      ]);
    });

    it("does not depend on the Entra or Google module: with both off it is the section's only item", () => {
      const withoutEither = allModules.filter((key) => key !== "entra" && key !== "google");
      const sections = workspaceFor(withAi, withoutEither);
      expect(workspacePaths(sections)).toEqual([
        "/workspace/overview",
        "/workspace/users",
        "/workspace/invitations",
        "/workspace/ai",
        "/workspace/assistant",
      ]);
      expect(sections.map((section) => section.label)).toEqual([undefined, "navigation.integrations"]);
    });
  });

  // Meta (Messenger design N9): the Owner's page, present while the module is on; it has no permission.
  describe("the Meta item", () => {
    const withMeta: readonly ModuleKey[] = [...allModules, "meta"];
    const metaItem = (context: NavVisibilityContext, modules: readonly ModuleKey[] = withMeta) =>
      workspacePaths(
        appNavSections(areaForKey("workspace"), modules, { ...context, enabledModules: modules }),
      ).includes("/workspace/meta");

    it("is the Owner's, behind the module, and has no permission of its own", () => {
      const item = areaForKey("workspace")
        .navSections.flatMap((section) => section.items)
        .find((one) => one.to === "/workspace/meta");
      expect(item).toMatchObject({ label: "navigation.meta", module: "meta", ownerOnly: true });
      expect(item?.requiredPermissions).toBeUndefined();
      expect(metaItem(ownerOnWorkspace)).toBe(true);
      expect(metaItem(ownerOnWorkspace, allModules)).toBe(false);
    });

    it("is not offered to anyone but an Owner, whatever they hold", () => {
      expect(metaItem({ ...visibility, permissions: ["*"] })).toBe(false);
      expect(metaItem({ ...visibility, permissions: ["*"], canManageAuthorization: true })).toBe(false);
    });
  });

  it("shows the settings pages to everyone and gives system admin no sidebar", () => {
    expect(
      appNavSections(areaForKey("settings"), allModules, visibility).flatMap((section) =>
        section.items.map((item) => item.to),
      ),
    ).toEqual(["/settings/profile", "/settings/security", "/settings/connections"]);
    expect(appNavSections(areaForKey("admin"), allModules, visibility)).toEqual([]);
  });

  it("shows Connections beside Profile and Security only while a connection provider's module is on", () => {
    const settings = areaForKey("settings");
    const item = settings.navSections
      .flatMap((section) => section.items)
      .find((one) => one.to === "/settings/connections");
    expect(item).toMatchObject({ label: "navigation.connections" });
    expect(item?.module).toBeUndefined();
    // The entry names exactly the modules the host's list of providers holds.
    expect(item?.modules).toEqual(connectionProviders.map((provider) => provider.module));
    const withoutEither = allModules.filter((key) => key !== "entra" && key !== "google");
    expect(
      appNavSections(settings, withoutEither, { ...visibility, enabledModules: withoutEither }).flatMap((section) =>
        section.items.map((one) => one.to),
      ),
    ).toEqual(["/settings/profile", "/settings/security"]);
    expect(navigationCatalog.en["navigation.connections"]).toBe("Connections");
    expect(navigationCatalog.nb["navigation.connections"]).toBe("Tilkoblinger");
  });

  // The way in for someone who sees Integrations alone: an Entra manager who is not an Owner has no
  // Workspace admin entry, as an authorization manager has Roles & access instead.
  it("offers an Entra manager Microsoft Entra ID in the avatar menu, while the module is on", () => {
    const menuPaths = (context: NavVisibilityContext) =>
      visibleNavSections(accountMenuSections, context).flatMap((section) => section.items.map((item) => item.to));
    const manager = { ...visibility, permissions: ["entra:manage"] };
    expect(menuPaths(manager)).toEqual(["/settings", "/docs", "/workspace/entra"]);
    expect(menuPaths({ ...ownerOnWorkspace, canManageAuthorization: true })).toEqual([
      "/settings",
      "/docs",
      "/workspace/overview",
      "/workspace/roles",
      "/workspace/entra",
      "/workspace/google",
    ]);
    const withoutEntra = allModules.filter((key) => key !== "entra");
    expect(menuPaths({ ...manager, enabledModules: withoutEntra })).toEqual(["/settings", "/docs"]);
    expect(menuPaths(visibility)).toEqual(["/settings", "/docs"]);
  });

  it("offers a Google manager Google Workspace in the avatar menu, while the module is on", () => {
    const menuPaths = (context: NavVisibilityContext) =>
      visibleNavSections(accountMenuSections, context).flatMap((section) => section.items.map((item) => item.to));
    const manager = { ...visibility, permissions: ["google:manage"] };
    expect(menuPaths(manager)).toEqual(["/settings", "/docs", "/workspace/google"]);
    const withoutGoogle = allModules.filter((key) => key !== "google");
    expect(menuPaths({ ...manager, enabledModules: withoutGoogle })).toEqual(["/settings", "/docs"]);
  });

  it("names the area in the header", () => {
    expect(appTitleLabel(areaForKey("settings"))).toBe("navigation.settings");
    expect(appTitleLabel(areaForKey("workspace"))).toBe("navigation.workspaceAdmin");
    expect(appTitleLabel(areaForKey("admin"))).toBe("navigation.systemAdmin");
  });
});

describe("activeAppKey", () => {
  it("takes the deepest match that carries an app", () => {
    expect(activeAppKey([{ staticData: {} }, { staticData: { app: "customers" } }, { staticData: {} }])).toBe(
      "customers",
    );
  });

  it("reads an area the same way", () => {
    expect(activeAppKey([{ staticData: {} }, { staticData: { app: "workspace" } }])).toBe("workspace");
  });

  it("is undefined when no match carries an app (public paths)", () => {
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
      ["calendar", false],
      ["customers", true],
      ["projects", false],
      ["time", false],
      ["expenses", false],
      ["staff", false],
      ["planning", false],
      ["invoices", false],
      ["orders", false],
      ["communications", false],
      ["knowledge", false],
      ["agents", false],
      ["products", false],
      ["inventory", false],
      ["manufacturing", false],
      ["energy", false],
    ]);
    expect(tiles.some((tile) => tile.current)).toBe(false);
  });

  it("shows only Home while permissions are still loading", () => {
    expect(switcherTiles(undefined, allModules, "home").map((tile) => tile.app.key)).toEqual(["home"]);
  });
});

describe("appTitleLabel", () => {
  it("names the app in the header, except Home, which shows the product title", () => {
    expect(appTitleLabel(appForKey("customers"))).toBe("navigation.customers");
    expect(appTitleLabel(appForKey("home"))).toBeUndefined();
    expect(appTitleLabel(undefined)).toBeUndefined();
  });
});

describe("appNavSections", () => {
  const visibility = { permissions: ["*"], isOwner: false, canManageAuthorization: false, enabledModules: allModules };

  it("returns the app's visible sections when its module is enabled", () => {
    const sections = appNavSections(appForKey("customers"), allModules, visibility);
    expect(sections.flatMap((section) => section.items.map((item) => item.to))).toEqual([
      "/customers",
      "/customers/contacts",
      "/customers/follow-ups",
    ]);
  });

  it("returns nothing for a disabled app, for Home, and off any app", () => {
    expect(
      appNavSections(appForKey("energy"), ["customers"], { ...visibility, enabledModules: ["customers"] }),
    ).toEqual([]);
    expect(appNavSections(appForKey("home"), allModules, visibility)).toEqual([]);
    expect(appNavSections(undefined, allModules, visibility)).toEqual([]);
  });

  it("still applies permission filtering inside an enabled app", () => {
    const sections = appNavSections(appForKey("customers"), allModules, {
      ...visibility,
      permissions: ["customers:view"],
    });
    expect(sections.flatMap((section) => section.items.map((item) => item.to))).toEqual(["/customers"]);
  });
});

// The Assistant (Assistant design S1, S8): an app with no permission of its own and no sidebar entries. The person
// has it or not, which the root layout learns from GET /assistant/availability and passes as the "assistant" feature.
describe("the Assistant app", () => {
  const assistantTiles = (
    features?: readonly ("ai" | "assistant")[],
    modules: readonly (typeof allModules)[number][] = allModules,
  ) => switcherTiles([], modules, "home", features).map((tile) => tile.app.key);

  it("is /assistant, called Assistant, behind its module and with no sidebar", () => {
    expect(appForKey("assistant")).toMatchObject({
      module: "assistant",
      label: "navigation.assistant",
      home: "/assistant",
      navSections: [],
      feature: "assistant",
    });
    expect(appForKey("assistant").requiredPermissions).toBeUndefined();
    expect(navigationCatalog.en["navigation.assistant"]).toBe("Assistant");
    expect(navigationCatalog.nb["navigation.assistant"]).toBe("Assistent");
  });

  it("has a tile only for a person who has the assistant", () => {
    expect(assistantTiles()).not.toContain("assistant");
    expect(assistantTiles(["ai"])).not.toContain("assistant");
    expect(assistantTiles(["assistant"])).toContain("assistant");
  });

  it("is muted, not hidden, when the installation did not mount the module", () => {
    const tile = switcherTiles(
      [],
      allModules.filter((key) => key !== "assistant"),
      "home",
      ["assistant"],
    ).find((one) => one.app.key === "assistant");
    expect(tile?.enabled).toBe(false);
  });

  it("is a Spotlight destination only for a person who has the assistant", () => {
    const spotlight = (features?: readonly ("ai" | "assistant")[]) =>
      visibleNavSections(spotlightNavSections, {
        permissions: ["*"],
        isOwner: true,
        canManageAuthorization: true,
        enabledModules: allModules,
        features,
      }).flatMap((section) => section.items.map((item) => item.to));
    expect(spotlight()).not.toContain("/assistant");
    expect(spotlight(["assistant"])).toContain("/assistant");
  });

  it("puts its Owner page next to AI models, while the module is on and the instance has an AI provider", () => {
    const owner: NavVisibilityContext = {
      permissions: ["*"],
      isOwner: true,
      canManageAuthorization: false,
      features: ["ai"],
    };
    const paths = (modules: readonly (typeof allModules)[number][]) =>
      appNavSections(areaForKey("workspace"), modules, { ...owner, enabledModules: modules }).flatMap((section) =>
        section.items.map((item) => item.to),
      );
    expect(paths(allModules)).toEqual([
      "/workspace/overview",
      "/workspace/users",
      "/workspace/invitations",
      "/workspace/entra",
      "/workspace/google",
      "/workspace/ai",
      "/workspace/assistant",
    ]);
    expect(paths(allModules.filter((key) => key !== "assistant"))).not.toContain("/workspace/assistant");
    const withoutAi = appNavSections(areaForKey("workspace"), allModules, {
      ...owner,
      features: [],
      enabledModules: allModules,
    });
    expect(withoutAi.flatMap((section) => section.items.map((item) => item.to))).not.toContain("/workspace/assistant");
    expect(navigationCatalog.nb["navigation.assistantSettings"]).toBe("Assistent");
  });
});
