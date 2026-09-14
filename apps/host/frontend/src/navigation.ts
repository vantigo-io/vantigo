import {
  IconAddressBook,
  IconBolt,
  IconBuildingSkyscraper,
  IconCategory,
  IconInbox,
  IconLayoutDashboard,
  IconMailbox,
  IconMailOff,
  IconPackage,
  IconSettings,
  IconShieldCheck,
  IconUsers,
} from "@tabler/icons-react";
import type { ComponentType } from "react";

// Relocated from the deleted api/tenant-capabilities.ts (task 2 of the
// frontend de-tenanting plan): this catalog is the single source of truth
// for which destinations belong to which module.
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
  placement?: "lower";
}

export const navSections: readonly NavSection[] = [
  {
    items: [
      {
        label: "navigation.dashboard",
        to: "/dashboard",
        icon: IconLayoutDashboard,
      },
    ],
  },
  {
    label: "navigation.customerWorkspace",
    items: [
      {
        label: "navigation.customers",
        to: "/customers",
        module: "customers",
        icon: IconUsers,
        requiredPermissions: ["customers:view"],
        searchStrategy: "customer-list",
      },
      {
        label: "navigation.contacts",
        to: "/contacts",
        module: "customers",
        icon: IconAddressBook,
        requiredPermissions: ["customers:contacts-view", "customers:associations-view"],
        searchStrategy: "customer-list",
      },
    ],
  },
  {
    label: "navigation.communications",
    items: [
      {
        label: "navigation.inbox",
        to: "/inbox",
        module: "communications",
        icon: IconInbox,
        requiredPermissions: ["communications:conversations-view"],
        searchStrategy: "inbox-list",
      },
      {
        label: "navigation.channels",
        to: "/communications/channels",
        module: "communications",
        icon: IconMailbox,
        requiredPermissions: ["communications:channels-manage"],
      },
      {
        label: "navigation.suppressions",
        to: "/communications/suppressions",
        module: "communications",
        icon: IconMailOff,
        requiredPermissions: ["communications:suppressions-manage"],
      },
    ],
  },
  {
    label: "navigation.catalog",
    items: [
      {
        label: "navigation.products",
        to: "/products",
        module: "products",
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
        module: "products",
        icon: IconCategory,
        requiredPermissions: ["products:categories-view"],
      },
    ],
  },
  {
    label: "navigation.energy",
    items: [
      {
        label: "navigation.meteringPoints",
        to: "/energy/metering-points",
        module: "energy",
        icon: IconBolt,
        requiredPermissions: ["energy:metering-points-view", "energy:meters-view"],
        searchStrategy: "energy-list",
      },
    ],
  },
  {
    label: "navigation.settingsAdministration",
    placement: "lower",
    items: [
      // Personal account settings (/settings) stay distinct from workspace
      // administration (/workspace): the latter is Owner-gated, the former is
      // open to every signed-in user.
      { label: "navigation.settings", to: "/settings", icon: IconSettings },
      {
        label: "navigation.adminDashboard",
        to: "/workspace/overview",
        icon: IconLayoutDashboard,
        ownerOnly: true,
      },
      {
        label: "navigation.rolesAccess",
        to: "/workspace/roles",
        icon: IconShieldCheck,
        capability: "authorization",
      },
      { label: "navigation.systemAdmin", to: "/admin", icon: IconBuildingSkyscraper, systemAdminOnly: true },
    ],
  },
];

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

export const visibleNavSections = ({
  permissions,
  isOwner,
  canManageAuthorization,
  isSystemAdmin = false,
  enabledModules,
}: NavVisibilityContext) =>
  navSections
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
