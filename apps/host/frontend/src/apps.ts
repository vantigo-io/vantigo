import {
  IconAddressBook,
  IconBolt,
  IconBriefcase,
  IconCategory,
  IconInbox,
  IconLayoutDashboard,
  IconMailbox,
  IconMailOff,
  IconPackage,
  IconReceiptTax,
  IconShieldCheck,
  IconShieldLock,
  IconUser,
  IconUserPlus,
  IconUsers,
} from "@tabler/icons-react";
import { accountMenuSections } from "./account-menu";
import {
  hasPermissions,
  type ModuleKey,
  type NavItem,
  type NavSection,
  type NavVisibilityContext,
  visibleNavSections,
} from "./navigation";

export type AppKey = "home" | ModuleKey;

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
  moduleApp("projects", "navigation.projects", IconBriefcase, "/projects", [
    {
      label: "navigation.projects",
      to: "/projects",
      icon: IconBriefcase,
      requiredPermissions: ["projects:access"],
      searchStrategy: "projects-list",
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
    {
      label: "navigation.taxCategories",
      to: "/products/tax-categories",
      icon: IconReceiptTax,
      requiredPermissions: ["products:tax-categories-view"],
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

/**
 * Everything the spotlight can navigate to: the dashboard (Home has no
 * sidebar, so it is in no app's sections), every app's sidebar, and the
 * avatar menu's destinations. Filtered per user at render time.
 */
export const spotlightNavSections: readonly NavSection[] = [
  { items: [{ label: "navigation.dashboard", to: "/dashboard", icon: IconLayoutDashboard }] },
  ...allNavSections,
  ...accountMenuSections,
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
): SwitcherTile[] =>
  apps
    .filter((app) => hasPermissions(permissions, app.requiredPermissions))
    .map((app) => ({ app, current: app.key === activeKey, enabled: isAppEnabled(app, enabledModules) }));
