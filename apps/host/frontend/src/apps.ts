import {
  IconAddressBook,
  IconAdjustments,
  IconBarcode,
  IconBolt,
  IconBook,
  IconBook2,
  IconBrandGoogle,
  IconBrandMessenger,
  IconBrandWindows,
  IconBriefcase,
  IconBuildingBank,
  IconBuildingFactory2,
  IconBuildingWarehouse,
  IconBulb,
  IconCalendar,
  IconCalendarDue,
  IconCalendarEvent,
  IconCalendarStats,
  IconCalendarTime,
  IconCalendarUser,
  IconCashBanknote,
  IconCategory,
  IconCertificate,
  IconChartBar,
  IconChartLine,
  IconChecklist,
  IconClipboardCheck,
  IconClipboardList,
  IconClock,
  IconClockExclamation,
  IconDeviceTablet,
  IconFileDollar,
  IconFileImport,
  IconFileInvoice,
  IconFilePlus,
  IconFiles,
  IconFlag,
  IconHammer,
  IconHeartHandshake,
  IconId,
  IconInbox,
  IconLayoutDashboard,
  IconListCheck,
  IconListDetails,
  IconMailbox,
  IconMailOff,
  IconMessageChatbot,
  IconPackage,
  IconPackageImport,
  IconPlugConnected,
  IconPuzzle,
  IconReceipt2,
  IconReceiptTax,
  IconRepeat,
  IconReportMoney,
  IconRobot,
  IconShieldCheck,
  IconShieldLock,
  IconShoppingCart,
  IconSparkles,
  IconTelescope,
  IconTemplate,
  IconTool,
  IconTransfer,
  IconTrash,
  IconTrendingDown,
  IconTruckDelivery,
  IconUser,
  IconUserPlus,
  IconUserQuestion,
  IconUserSearch,
  IconUsers,
  IconUsersGroup,
  IconZoomExclamation,
} from "@tabler/icons-react";
import { type AgentsNavItem, agentsNavItems } from "@vantigo/agents-ui";
import { type InventoryNavItem, inventoryNavItems } from "@vantigo/inventory-ui";
import { type KnowledgeNavItem, knowledgeNavItems } from "@vantigo/knowledge-ui";
import { type ManufacturingNavItem, manufacturingNavItems } from "@vantigo/manufacturing-ui";
import { type OrdersNavItem, ordersNavItems } from "@vantigo/orders-ui";
import { type PlanningNavItem, planningNavItems } from "@vantigo/planning-ui";
import { type StaffNavItem, staffNavItems } from "@vantigo/staff-ui";
import { accountMenuSections } from "./account-menu";
import {
  hasPermissions,
  type ModuleKey,
  type NavFeature,
  type NavItem,
  type NavSection,
  type NavVisibilityContext,
  visibleNavSections,
} from "./navigation";

/**
 * The modules that own an app (a switcher tile and a sidebar). Entra, Google
 * and Meta are modules without one: their pages are Microsoft Entra ID, Google
 * Workspace and Meta under *Integrations* in Workspace admin (Integrations
 * design T2, Google Workspace design G9, G16, Messenger design N9), so they
 * are not here and `appForKey("entra")`, `appForKey("google")` and
 * `appForKey("meta")` are type errors.
 */
export type ModuleWithApp = Exclude<ModuleKey, "entra" | "google" | "meta">;

export type AppKey = "home" | ModuleWithApp;

/**
 * The administration areas: like apps they own a URL prefix, a header title
 * and a sidebar, but they have no switcher tile (the avatar menu leads to
 * them) and no module to be enabled.
 */
export type AreaKey = "settings" | "workspace" | "admin";

declare module "@tanstack/react-router" {
  interface StaticDataRouteOption {
    /** The app or area a route subtree belongs to. Set only on layout routes and the dashboard. */
    app?: AppKey | AreaKey;
  }
}

export interface AreaDefinition {
  key: AreaKey;
  /** i18n key in the host navigation catalog. */
  label: string;
  /** Where the area's own index route redirects. */
  home: string;
  /** Sidebar sections; empty means the area renders without a sidebar. */
  navSections: readonly NavSection[];
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
  /**
   * The tile is hidden unless this instance fact holds for the caller. The
   * Assistant has no permission of its own (design S8): the person has it or
   * not, and the root layout learns that from GET /assistant/availability.
   */
  feature?: NavFeature;
}

const permissionsUnlocking = (items: readonly NavItem[]) => [
  ...new Set(items.flatMap((item) => item.requiredPermissions ?? [])),
];

/** A module app: one unlabeled sidebar section, a tile shown when any of its entries would be. */
const moduleApp = (
  key: ModuleWithApp,
  label: string,
  icon: NavItem["icon"],
  home: string,
  items: readonly Omit<NavItem, "module">[],
): AppDefinition => {
  const tagged = items.map((item) => ({ ...item, module: key }));
  return {
    key,
    module: key,
    label,
    icon,
    home,
    navSections: [{ items: tagged }],
    requiredPermissions: permissionsUnlocking(tagged),
  };
};

/**
 * The host's half of a Staff sidebar entry. Where each destination is and
 * which permissions offer and guard it are the package's (`staffNavItems`),
 * so its pages' links and this sidebar cannot drift apart; the label and the
 * icon are the host's, as every app's are. People is "Staff directory" here
 * rather than Time's bare "People" for the reason Expense approvals' comment
 * gives: Spotlight flattens every app's destinations into one list.
 */
const staffNavPresentation: Record<StaffNavItem["key"], Pick<NavItem, "label" | "icon">> = {
  myPage: { label: "navigation.staffMyPage", icon: IconId },
  people: { label: "navigation.staffPeople", icon: IconUsersGroup },
  absence: { label: "navigation.staffAbsence", icon: IconCalendarEvent },
  competence: { label: "navigation.staffCompetence", icon: IconCertificate },
  groups: { label: "navigation.staffGroups", icon: IconUsers },
  settings: { label: "navigation.staffSettings", icon: IconAdjustments },
};

/**
 * The host's half of a Planning sidebar entry, as Staff's above: where each
 * destination is and which permission offers it are the package's
 * (`planningNavItems`); the label and the icon are the host's.
 */
const planningNavPresentation: Record<PlanningNavItem["key"], Pick<NavItem, "label" | "icon">> = {
  board: { label: "navigation.planningBoard", icon: IconCalendarStats },
  requests: { label: "navigation.planningRequests", icon: IconUserSearch },
  intake: { label: "navigation.planningIntake", icon: IconTelescope },
  unbooked: { label: "navigation.planningUnbooked", icon: IconUserQuestion },
  wishes: { label: "navigation.planningWishes", icon: IconHeartHandshake },
  myPlanning: { label: "navigation.planningMyPlanning", icon: IconCalendarUser },
  settings: { label: "navigation.planningSettings", icon: IconAdjustments },
};

/**
 * The host's half of an Orders sidebar entry, as Planning's above: where each
 * destination is and which permissions offer it are the package's
 * (`ordersNavItems`); the label and the icon are the host's. Orders is "Sales
 * orders" in English, as People is "Staff directory": Spotlight flattens every
 * app's destinations into one list, where a bare "Orders" would name the app.
 */
const ordersNavPresentation: Record<OrdersNavItem["key"], Pick<NavItem, "label" | "icon">> = {
  quotes: { label: "navigation.ordersQuotes", icon: IconFileDollar },
  orders: { label: "navigation.ordersOrders", icon: IconShoppingCart },
  deliveries: { label: "navigation.ordersDeliveries", icon: IconTruckDelivery },
  subscriptions: { label: "navigation.ordersSubscriptions", icon: IconRepeat },
  analysis: { label: "navigation.ordersAnalysis", icon: IconChartBar },
  templates: { label: "navigation.ordersTemplates", icon: IconTemplate },
  settings: { label: "navigation.ordersSettings", icon: IconAdjustments },
};

/**
 * The host's half of a Knowledge sidebar entry, as Planning's above: where
 * each destination is and which permission offers it are the package's
 * (`knowledgeNavItems`); the label and the icon are the host's. The trash is
 * "Knowledge trash" in English for the reason Orders' comment gives.
 */
const knowledgeNavPresentation: Record<KnowledgeNavItem["key"], Pick<NavItem, "label" | "icon">> = {
  articles: { label: "navigation.knowledgeArticles", icon: IconBook2 },
  trash: { label: "navigation.knowledgeTrash", icon: IconTrash },
};

/**
 * The host's half of an Agents sidebar entry, as Orders' above: where each
 * destination is and which permission offers it are the package's
 * (`agentsNavItems`); the label and the icon are the host's.
 */
const agentsNavPresentation: Record<AgentsNavItem["key"], Pick<NavItem, "label" | "icon">> = {
  agents: { label: "navigation.agentsList", icon: IconRobot },
  review: { label: "navigation.agentsReview", icon: IconChecklist },
  settings: { label: "navigation.agentsSettings", icon: IconAdjustments },
};

/**
 * The host's half of an Inventory sidebar entry, as Planning's above: where
 * each destination is, the search it opens with and which permissions offer
 * it are the package's (`inventoryNavItems`); the label and the icon are the
 * host's. Receipts is the movements list filtered to receipts (design §13.3),
 * so it shares Movements' path and carries `kind=receipt`.
 */
const inventoryNavPresentation: Record<InventoryNavItem["key"], Pick<NavItem, "label" | "icon">> = {
  overview: { label: "navigation.inventoryOverview", icon: IconBuildingWarehouse },
  lots: { label: "navigation.inventoryLots", icon: IconBarcode },
  expiring: { label: "navigation.inventoryExpiring", icon: IconCalendarDue },
  transfers: { label: "navigation.inventoryTransfers", icon: IconTruckDelivery },
  movements: { label: "navigation.inventoryMovements", icon: IconTransfer },
  receipts: { label: "navigation.inventoryReceipts", icon: IconPackageImport },
  counts: { label: "navigation.inventoryCounts", icon: IconClipboardCheck },
  lowStock: { label: "navigation.inventoryLowStock", icon: IconTrendingDown },
  import: { label: "navigation.inventoryImport", icon: IconFileImport },
};

/**
 * The host's half of a Manufacturing sidebar entry, as Inventory's above:
 * where each destination is and which permission offers it are the
 * package's (`manufacturingNavItems`, manufacturing design M7); the label and
 * the icon are the host's.
 */
const manufacturingNavPresentation: Record<ManufacturingNavItem["key"], Pick<NavItem, "label" | "icon">> = {
  overview: { label: "navigation.manufacturingOverview", icon: IconBuildingFactory2 },
  orders: { label: "navigation.manufacturingOrders", icon: IconClipboardList },
  suggestions: { label: "navigation.manufacturingSuggestions", icon: IconBulb },
  unbuilds: { label: "navigation.manufacturingUnbuilds", icon: IconPuzzle },
  shopFloor: { label: "navigation.manufacturingShopFloor", icon: IconHammer },
  schedule: { label: "navigation.manufacturingSchedule", icon: IconCalendarTime },
  deviations: { label: "navigation.manufacturingDeviations", icon: IconZoomExclamation },
  boms: { label: "navigation.manufacturingBoms", icon: IconListDetails },
  workCenters: { label: "navigation.manufacturingWorkCenters", icon: IconTool },
};

/** Every app, in switcher order. Home has no module and no sidebar. */
export const apps: readonly AppDefinition[] = [
  { key: "home", label: "navigation.home", icon: IconLayoutDashboard, home: "/dashboard", navSections: [] },
  // The Assistant app lists the person's conversations itself, so it has no sidebar entries to offer;
  // its tile and its Spotlight destination appear only for a person who has the assistant.
  {
    key: "assistant",
    module: "assistant",
    label: "navigation.assistant",
    icon: IconMessageChatbot,
    home: "/assistant",
    navSections: [],
    feature: "assistant",
  },
  moduleApp("calendar", "navigation.calendar", IconCalendar, "/calendar", [
    {
      label: "navigation.calendar",
      to: "/calendar",
      icon: IconCalendar,
      requiredPermissions: ["calendar:access"],
    },
    {
      // The person's own calendar addresses and subscriptions (calendar feed design D9).
      label: "navigation.myCalendar",
      to: "/calendar/settings",
      icon: IconAdjustments,
      requiredPermissions: ["calendar:access"],
    },
  ]),
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
    {
      // The page's API needs customers:timeline-view AND customers:view, but
      // `requiredPermissions` is any-of (`hasPermissions`), so listing both
      // would offer Follow-ups to a caller holding only customers:view. Only
      // the sensitive half is listed: everyone with timeline-view has view in
      // practice, and the narrower miss is the safe direction. What that costs
      // is the other direction: a caller holding customers:timeline-view but
      // NOT customers:view is still shown the nav entry and the link, and the
      // list request behind the page then 403s — the page's own error state
      // says so in words rather than showing an empty table, which is why the
      // miss is acceptable rather than merely rare. The Done tick inside asks
      // for customers:timeline-manage separately (design D3).
      label: "navigation.followUps",
      to: "/customers/follow-ups",
      icon: IconFlag,
      requiredPermissions: ["customers:timeline-view"],
    },
  ]),
  moduleApp("projects", "navigation.projects", IconBriefcase, "/projects", [
    {
      label: "navigation.projects",
      to: "/projects",
      icon: IconBriefcase,
      requiredPermissions: ["projects:access"],
      searchStrategy: "projects-list",
    },
    {
      // Tasks follow the project's roles rather than a permission of their
      // own, so seeing the app is seeing your own tasks across it.
      label: "navigation.myTasks",
      to: "/projects/my-tasks",
      icon: IconListCheck,
      requiredPermissions: ["projects:access"],
    },
    {
      // The portfolio's rows are decided by the API on financial rights (a
      // project's own manager, projects:view-financials, or manage-all), which
      // is not one permission the sidebar can name — a plain project manager
      // holds none of the global ones and still has rows here. So the entry is
      // offered on projects:access, the app's own permission, and the page's
      // own empty state covers a caller with nothing to see.
      label: "navigation.projectsEconomy",
      to: "/projects/economy",
      icon: IconReportMoney,
      requiredPermissions: ["projects:access"],
    },
    {
      // The catalog is the people's who own it: every template write asks
      // projects:create on top of projects:access, the same two as starting
      // a project, so the entry and the route ask both. Everyone else in the
      // app still sees every template where it is used, in Use template on a
      // project's Tasks tab. projects:create rides in requiredAllPermissions so
      // the app tile keeps following projects:access alone.
      label: "navigation.projectTemplates",
      to: "/projects/templates",
      icon: IconTemplate,
      requiredPermissions: ["projects:access"],
      requiredAllPermissions: ["projects:create"],
    },
  ]),
  moduleApp("time", "navigation.time", IconClock, "/time", [
    {
      label: "navigation.myWeek",
      to: "/time",
      icon: IconClock,
      requiredPermissions: ["time:access"],
      searchStrategy: "time-week",
    },
    {
      // The queue is offered in the sidebar to the approvers a permission
      // names, but a project manager approves their own project's hours
      // through their role alone. They reach the same page from the
      // dashboard's attention list, so the guard asks only for the app —
      // the backend answers 403 to a caller who approves nothing.
      label: "navigation.approvals",
      to: "/time/approvals",
      icon: IconChecklist,
      requiredPermissions: ["time:approve"],
      guardPermissions: ["time:access"],
    },
    {
      label: "navigation.people",
      to: "/time/people",
      icon: IconUsers,
      requiredPermissions: ["time:view-all"],
    },
    {
      label: "navigation.timeSettings",
      to: "/time/settings",
      icon: IconAdjustments,
      requiredPermissions: ["time:manage"],
    },
  ]),
  moduleApp("expenses", "navigation.expenses", IconReceipt2, "/expenses", [
    {
      label: "navigation.myExpenses",
      to: "/expenses",
      icon: IconReceipt2,
      requiredPermissions: ["expenses:access"],
    },
    {
      // Offered in the sidebar to the dedicated approvers, but a project
      // manager approves their own project's expenses through their role
      // alone and reaches the same queue from the dashboard's attention list
      // or by pasting the URL — the same seam as Time's approval queue. Named
      // "Expense approvals" rather than the bare "Approvals" Time uses: within
      // Time's own sidebar section the word is unambiguous, but Spotlight
      // flattens every app's destinations into one list, where two identical
      // labels would be indistinguishable.
      label: "navigation.expenseApprovals",
      to: "/expenses/approvals",
      icon: IconChecklist,
      requiredPermissions: ["expenses:approve"],
      guardPermissions: ["expenses:access"],
    },
    {
      label: "navigation.reimbursements",
      to: "/expenses/reimbursements",
      icon: IconCashBanknote,
      requiredPermissions: ["expenses:manage"],
    },
    {
      label: "navigation.expensesSettings",
      to: "/expenses/settings",
      icon: IconAdjustments,
      requiredPermissions: ["expenses:manage"],
    },
  ]),
  {
    ...moduleApp(
      "staff",
      "navigation.staff",
      IconUsersGroup,
      "/staff",
      staffNavItems.map(({ key, to, requiredPermissions, guardPermissions }) => ({
        ...staffNavPresentation[key],
        to,
        requiredPermissions,
        guardPermissions,
      })),
    ),
    // The tile asks for the app itself, not any entry's permission: the
    // server refuses every Staff call without staff:access, so a role with
    // staff:view or staff:manage alone would open an app that answers 403.
    requiredPermissions: ["staff:access"],
  },
  {
    ...moduleApp(
      "planning",
      "navigation.planning",
      IconCalendarStats,
      "/planning",
      planningNavItems.map(({ key, to, requiredPermissions }) => ({
        ...planningNavPresentation[key],
        to,
        requiredPermissions,
        // The board is offered to planning:view, but its URL is the app's
        // prefix, which a person's own timeline (/planning/people/$personId)
        // lives under too — planning:access's, the server answering 404 for
        // anyone else's. So the guard asks for the app here; the board's own
        // route sends a caller without planning:view to My planning.
        guardPermissions: key === "board" ? ["planning:access"] : undefined,
      })),
    ),
    // The tile asks for the app itself, as Staff's does: every Planning call
    // requires planning:access, whatever else the caller holds.
    requiredPermissions: ["planning:access"],
  },
  moduleApp("invoices", "navigation.invoices", IconFileInvoice, "/invoices", [
    {
      // Invoicing is a finance job, not per-customer (invoices foundation
      // design D1): invoices:access reads every document, so the list is the
      // whole app's entry.
      label: "navigation.invoices",
      to: "/invoices",
      icon: IconFileInvoice,
      requiredPermissions: ["invoices:access"],
    },
    {
      // The quick invoice (invoices payments and reminders design D17, D22):
      // a customer and a few lines, created and issued in one step on site.
      // Its API asks for invoices:access, invoices:create and invoices:issue
      // together (meta's canQuickInvoice); requiredPermissions is any-of, so
      // the app's own permission is named there and the other two must all
      // be held as well, so neither a drafter nor an issuer alone is offered
      // a page that would only say "not allowed".
      label: "navigation.invoiceQuick",
      to: "/invoices/quick",
      icon: IconFilePlus,
      requiredPermissions: ["invoices:access"],
      requiredAllPermissions: ["invoices:create", "invoices:issue"],
    },
    {
      // The journal proves the series has no gaps (D11); everyone who reads
      // invoices reads it.
      label: "navigation.invoiceJournal",
      to: "/invoices/journal",
      icon: IconBook,
      requiredPermissions: ["invoices:access"],
    },
    {
      // The overdue list (invoices payments and reminders design D12, D22):
      // every reader of invoices reads it. Sending reminders and printing
      // paper letters are the page's own, behind meta's canRunReminders
      // (invoices:payments); the run's and the paper letters' pages fall
      // under the list's /invoices entry and say "not allowed" themselves.
      label: "navigation.invoiceOverdue",
      to: "/invoices/overdue",
      icon: IconClockExclamation,
      requiredPermissions: ["invoices:access"],
    },
    {
      // Billing runs (energy consumption billing design D10, D11): one
      // invoice per customer with consumption due, made by the worker. Making
      // and cancelling a run ask for invoices:access and invoices:create
      // together; requiredPermissions is any-of, so the app's own permission
      // is named there and invoices:create must be held as well. Issuing
      // with a run is the page's own, behind meta's canIssue; a run's page
      // falls under this entry.
      label: "navigation.invoiceBillingRuns",
      to: "/invoices/billing-runs",
      icon: IconFiles,
      requiredPermissions: ["invoices:access"],
      requiredAllPermissions: ["invoices:create"],
    },
    {
      // The bank files and the exception queue (invoices payments and
      // reminders design D22): invoices:payments alone — hasPermissions is
      // any-of, so listing invoices:access too would offer it to every
      // reader, who would then meet 403s.
      label: "navigation.invoicePayments",
      to: "/invoices/payments",
      icon: IconBuildingBank,
      requiredPermissions: ["invoices:payments"],
    },
    {
      label: "navigation.invoiceSettings",
      to: "/invoices/settings",
      icon: IconAdjustments,
      requiredPermissions: ["invoices:manage"],
    },
  ]),
  // Every Orders call asks for orders:access (orders design §8), so the tile
  // asks for it alone; Analysis needs orders:analysis-view, Templates and
  // Settings orders:settings as well.
  {
    ...moduleApp(
      "orders",
      "navigation.orders",
      IconShoppingCart,
      "/orders",
      ordersNavItems.map(({ key, to, requiredPermissions, requiredAllPermissions }) => ({
        ...ordersNavPresentation[key],
        to,
        requiredPermissions,
        requiredAllPermissions,
      })),
    ),
    requiredPermissions: ["orders:access"],
  },
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
  // Every Knowledge call asks for knowledge:articles (knowledge design K2);
  // what a person may do with an article is the article's own access (K4).
  moduleApp(
    "knowledge",
    "navigation.knowledge",
    IconBook2,
    "/knowledge",
    knowledgeNavItems.map(({ key, to, requiredPermissions }) => ({
      ...knowledgeNavPresentation[key],
      to,
      requiredPermissions,
    })),
  ),
  // Every Agents screen is read with agents:view; the review queue is
  // agents:review's, so the app is offered to a reviewer who may not view the
  // agents; the settings and every write need agents:manage (virtual employees
  // design V10, V20).
  {
    ...moduleApp(
      "agents",
      "navigation.agents",
      IconRobot,
      "/agents",
      agentsNavItems.map(({ key, to, requiredPermissions }) => ({
        ...agentsNavPresentation[key],
        to,
        requiredPermissions,
      })),
    ),
    requiredPermissions: ["agents:view", "agents:review"],
  },
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
    {
      label: "navigation.taxCategories",
      to: "/products/tax-categories",
      icon: IconReceiptTax,
      requiredPermissions: ["products:tax-categories-view"],
    },
  ]),
  // The home needs inventory:locations-view or inventory:stock-view (design
  // §5), which is what Overview declares. Counts and Import are offered by
  // counts-manage and stock-adjust but need both views as well, so neither
  // alone shows anything: the tile asks for the views alone, not for the
  // union of the entries' permissions.
  {
    ...moduleApp(
      "inventory",
      "navigation.inventory",
      IconBuildingWarehouse,
      "/inventory",
      inventoryNavItems.map(({ key, to, search, requiredPermissions, requiredAllPermissions }) => ({
        ...inventoryNavPresentation[key],
        to,
        search,
        requiredPermissions,
        requiredAllPermissions,
      })),
    ),
    requiredPermissions: ["inventory:locations-view", "inventory:stock-view"],
  },
  // Every Manufacturing read asks for manufacturing:access (manufacturing
  // design §8), which offers each of the app's entries.
  moduleApp(
    "manufacturing",
    "navigation.manufacturing",
    IconBuildingFactory2,
    "/manufacturing",
    manufacturingNavItems.map(({ key, to, requiredPermissions }) => ({
      ...manufacturingNavPresentation[key],
      to,
      requiredPermissions,
    })),
  ),
  moduleApp("energy", "navigation.energy", IconBolt, "/energy", [
    {
      label: "navigation.meteringPoints",
      to: "/energy/metering-points",
      icon: IconBolt,
      requiredPermissions: ["energy:metering-points-view", "energy:meters-view"],
      searchStrategy: "energy-list",
    },
    {
      label: "navigation.prices",
      to: "/energy/prices",
      icon: IconChartLine,
      requiredPermissions: ["energy:prices-view"],
    },
    {
      label: "navigation.tariffs",
      to: "/energy/tariffs",
      icon: IconReportMoney,
      requiredPermissions: ["energy:tariffs-view"],
    },
  ]),
];

/** Every sidebar destination across apps, for consumers that span apps (spotlight, permission guard). */
export const allNavSections: readonly NavSection[] = apps.flatMap((app) => app.navSections);

/**
 * Everything the spotlight can navigate to: the dashboard (Home has no
 * sidebar, so it is in no app's sections), every app's sidebar, and the
 * avatar menu's destinations. Filtered per user at render time.
 */
export const spotlightNavSections: readonly NavSection[] = [
  { items: [{ label: "navigation.dashboard", to: "/dashboard", icon: IconLayoutDashboard }] },
  {
    items: [
      {
        label: "navigation.assistant",
        to: "/assistant",
        icon: IconMessageChatbot,
        module: "assistant",
        feature: "assistant",
      },
    ],
  },
  ...allNavSections,
  // External destinations open a new tab, which is not what a Spotlight jump does.
  ...accountMenuSections.map((section) => ({ ...section, items: section.items.filter((item) => !item.external) })),
];

/**
 * The administration areas, reached from the avatar menu. Their sidebar
 * entries carry the same visibility flags the account menu uses, so an
 * Owner sees the whole workspace area while an authorization manager who is
 * not an Owner sees only Roles & access. System admin is a single page and,
 * like Home, renders without a sidebar.
 */
export const areas: readonly AreaDefinition[] = [
  {
    key: "settings",
    label: "navigation.settings",
    home: "/settings/profile",
    navSections: [
      {
        items: [
          { label: "navigation.profile", to: "/settings/profile", icon: IconUser },
          { label: "navigation.security", to: "/settings/security", icon: IconShieldLock },
          // The person's own connections, one card per provider in lib/connections.tsx (Outlook
          // connection design M8, Google Workspace design G9): shown while any provider's module is on.
          {
            label: "navigation.connections",
            to: "/settings/connections",
            icon: IconPlugConnected,
            modules: ["entra", "google"],
          },
        ],
      },
    ],
  },
  {
    key: "workspace",
    label: "navigation.workspaceAdmin",
    home: "/workspace/overview",
    navSections: [
      {
        items: [
          { label: "navigation.overview", to: "/workspace/overview", icon: IconLayoutDashboard, ownerOnly: true },
          { label: "navigation.users", to: "/workspace/users", icon: IconUsers, ownerOnly: true },
          { label: "navigation.invitations", to: "/workspace/invitations", icon: IconUserPlus, ownerOnly: true },
          {
            label: "navigation.rolesAccess",
            to: "/workspace/roles",
            icon: IconShieldCheck,
            capability: "authorization",
          },
          // The shared shop-floor stations (manufacturing phase 2 M129): registered, paired and
          // revoked behind identity:manage, while Manufacturing is on.
          {
            label: "navigation.stations",
            to: "/workspace/stations",
            icon: IconDeviceTablet,
            module: "manufacturing",
            requiredPermissions: ["identity:manage"],
          },
        ],
      },
      // The tenant's connections to systems elsewhere (Integrations design T2).
      // Microsoft Entra ID is the connection to the tenant (Entra design D15), in
      // four tabs, behind entra:manage while the module is on. The Owner-only
      // steps are the tabs' to show read-only; the server refuses their writes
      // to anyone else. A module takes an item here as Entra's Connections page
      // takes one under Settings.
      {
        label: "navigation.integrations",
        items: [
          {
            label: "navigation.entra",
            to: "/workspace/entra",
            icon: IconBrandWindows,
            module: "entra",
            requiredPermissions: ["entra:manage"],
          },
          // Google Workspace is the connection to the organisation (Google Workspace design G16),
          // in four tabs, behind google:manage while the module is on, as Entra's is.
          {
            label: "navigation.google",
            to: "/workspace/google",
            icon: IconBrandGoogle,
            module: "google",
            requiredPermissions: ["google:manage"],
          },
          // Meta is the connection to Facebook Messenger (Messenger design N9): the Owner's, while
          // the module is on. It has no permission of its own.
          {
            label: "navigation.meta",
            to: "/workspace/meta",
            icon: IconBrandMessenger,
            ownerOnly: true,
            module: "meta",
          },
          // The carrier accounts Orders books shipments with (orders design O51), behind
          // orders:carriers-manage while the module is on.
          {
            label: "navigation.shipping",
            to: "/workspace/shipping",
            icon: IconTruckDelivery,
            module: "orders",
            requiredPermissions: ["orders:carriers-manage"],
          },
          // The bank Invoices reads incoming payments from (invoices bank feed design F9), behind
          // invoices:manage while the module is on.
          {
            label: "navigation.bank",
            to: "/workspace/bank",
            icon: IconBuildingBank,
            module: "invoices",
            requiredPermissions: ["invoices:manage"],
            requiredAllPermissions: ["invoices:access"],
          },
          // The AI plan Vantigo provisioned and the model each task uses (Integrations
          // design T13): the Owner's, and present only while the instance has an AI
          // provider, which the root layout learns from GET /owner/ai/settings.
          { label: "navigation.aiModels", to: "/workspace/ai", icon: IconSparkles, ownerOnly: true, feature: "ai" },
          // Who has the assistant, how long conversations are kept and what it may use (Assistant
          // design S8, S16): the Owner's, while the module is on and the instance has an AI provider.
          {
            label: "navigation.assistantSettings",
            to: "/workspace/assistant",
            icon: IconMessageChatbot,
            ownerOnly: true,
            module: "assistant",
            feature: "ai",
          },
        ],
      },
    ],
  },
  { key: "admin", label: "navigation.systemAdmin", home: "/admin", navSections: [] },
];

export const appForKey = (key: AppKey): AppDefinition => {
  const app = apps.find((candidate) => candidate.key === key);
  if (!app) throw new Error(`unknown app "${key}"`);
  return app;
};

/** The app or area behind a key: what the root needs for the header title and sidebar. */
export const areaForKey = (key: AppKey | AreaKey): AppDefinition | AreaDefinition => {
  const area = [...apps, ...areas].find((candidate) => candidate.key === key);
  if (!area) throw new Error(`unknown app or area "${key}"`);
  return area;
};

/**
 * The label key of the app another module's name names — a lot's hold put by
 * `manufacturing` reads as the sidebar's Manufacturing — undefined for a name
 * no app has, which a screen then shows as it is.
 */
export const moduleLabelKey = (module: string): string | undefined =>
  apps.find((candidate) => candidate.key === module)?.label;

export const isArea = (value: AppDefinition | AreaDefinition): value is AreaDefinition =>
  !("module" in value) && value.key !== "home";

/** Whether the app's module is enabled; Home and the areas have no module and are always enabled. */
export const isAppEnabled = (app: AppDefinition | AreaDefinition, enabledModules: readonly ModuleKey[]) =>
  isArea(app) || app.module === undefined || enabledModules.includes(app.module);

/** The header title for an app or area: its label, except Home, which shows the product title (undefined). */
export const appTitleLabel = (app: AppDefinition | AreaDefinition | undefined): string | undefined =>
  app && app.key !== "home" ? app.label : undefined;

/**
 * The sidebar sections to render: the active app's or area's visible
 * sections when it exists and is enabled; nothing otherwise (public paths,
 * or an app the installation turned off).
 */
export const appNavSections = (
  app: AppDefinition | AreaDefinition | undefined,
  enabledModules: readonly ModuleKey[],
  visibility: NavVisibilityContext,
): NavSection[] => (app && isAppEnabled(app, enabledModules) ? visibleNavSections(app.navSections, visibility) : []);

/** The app or area of the deepest matched route that declares one; undefined on public paths. */
export const activeAppKey = (
  matches: ReadonlyArray<{ staticData?: { app?: AppKey | AreaKey } }>,
): AppKey | AreaKey | undefined => {
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
  activeKey: AppKey | AreaKey | undefined,
  features: readonly NavFeature[] = [],
): SwitcherTile[] =>
  apps
    .filter((app) => hasPermissions(permissions, app.requiredPermissions))
    .filter((app) => !app.feature || features.includes(app.feature))
    .map((app) => ({ app, current: app.key === activeKey, enabled: isAppEnabled(app, enabledModules) }));
