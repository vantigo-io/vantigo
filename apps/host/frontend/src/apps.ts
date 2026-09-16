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
import { accountMenuSections } from "./account-menu";
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
