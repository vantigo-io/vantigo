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
import type { ModuleKey } from "./api/tenant-capabilities";

export interface NavItem {
  label: string;
  to: string;
  icon: ComponentType<{ size?: number | string; stroke?: number | string }>;
  ownerOnly?: boolean;
  systemAdminOnly?: boolean;
  capability?: "authorization";
  requiredPermissions?: readonly string[];
  /** The tenant module that must be enabled for this destination. */
  module?: ModuleKey;
  /** Business destinations are prefixed with the active tenant slug. */
  tenantScoped?: boolean;
  /** Search defaults used when Spotlight opens this destination. */
  searchStrategy?: "customer-list" | "inbox-list" | "products-list" | "energy-list";
}
export interface NavSection {
  label: string;
  items: readonly NavItem[];
  placement?: "lower";
}

export const navSections: readonly NavSection[] = [
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
        tenantScoped: true,
      },
      {
        label: "navigation.contacts",
        to: "/contacts",
        module: "customers",
        icon: IconAddressBook,
        requiredPermissions: ["customers:contacts-view", "customers:associations-view"],
        searchStrategy: "customer-list",
        tenantScoped: true,
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
        tenantScoped: true,
      },
      {
        label: "navigation.channels",
        to: "/communications/channels",
        module: "communications",
        icon: IconMailbox,
        requiredPermissions: ["communications:channels-manage"],
        tenantScoped: true,
      },
      {
        label: "navigation.suppressions",
        to: "/communications/suppressions",
        module: "communications",
        icon: IconMailOff,
        requiredPermissions: ["communications:suppressions-manage"],
        tenantScoped: true,
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
        tenantScoped: true,
      },
      {
        label: "navigation.categories",
        to: "/products/categories",
        module: "products",
        icon: IconCategory,
        requiredPermissions: ["products:categories-view"],
        tenantScoped: true,
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
        tenantScoped: true,
      },
    ],
  },
  {
    label: "navigation.settingsAdministration",
    placement: "lower",
    items: [
      { label: "navigation.settings", to: "/settings", icon: IconSettings },
      {
        label: "navigation.adminDashboard",
        to: "/settings/overview",
        icon: IconLayoutDashboard,
        ownerOnly: true,
        tenantScoped: true,
      },
      {
        label: "navigation.rolesAccess",
        to: "/settings/roles",
        icon: IconShieldCheck,
        capability: "authorization",
        tenantScoped: true,
      },
      { label: "navigation.systemAdmin", to: "/admin", icon: IconBuildingSkyscraper, systemAdminOnly: true },
    ],
  },
];

export const tenantPath = (tenantSlug: string | undefined, path: string) =>
  tenantSlug && path !== "/" ? `/${encodeURIComponent(tenantSlug)}${path}` : path;

export const navSectionsForTenant = (tenantSlug?: string): readonly NavSection[] =>
  navSections.map((section) => ({
    ...section,
    items: section.items.map((item) => ({
      ...item,
      to: item.tenantScoped ? tenantPath(tenantSlug, item.to) : item.to,
    })),
  }));

export const hasPermissions = (permissions: string[] | undefined, required?: readonly string[]) =>
  !required?.length ||
  permissions?.includes("*") === true ||
  required.some((permission) => permissions?.includes(permission));

export interface NavVisibilityContext {
  permissions: string[] | undefined;
  isOwner: boolean;
  canManageAuthorization: boolean;
  tenantSlug?: string;
  isSystemAdmin?: boolean;
  /** Enabled module keys for the active tenant; undefined while unknown (hides module destinations). */
  enabledModules?: readonly ModuleKey[];
}

export const visibleNavSections = ({
  permissions,
  isOwner,
  canManageAuthorization,
  tenantSlug,
  isSystemAdmin = false,
  enabledModules,
}: NavVisibilityContext) =>
  navSectionsForTenant(tenantSlug)
    .map((section) => ({
      ...section,
      items: section.items.filter(
        (item) =>
          (!item.ownerOnly || isOwner) &&
          (!item.systemAdminOnly || isSystemAdmin) &&
          (!item.capability || canManageAuthorization) &&
          // Tenant destinations stay hidden until a tenant is selected.
          (!item.tenantScoped || !!tenantSlug) &&
          (!item.module || enabledModules?.includes(item.module) === true) &&
          hasPermissions(permissions, item.requiredPermissions),
      ),
    }))
    .filter((section) => section.items.length > 0);

/** Select the first accessible business destination for the authenticated landing route. */
export const firstAuthorizedIntegratedAppDestination = (context: NavVisibilityContext) =>
  visibleNavSections(context)
    .filter((section) => section.placement !== "lower")
    .flatMap((section) => section.items)[0]?.to;

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

export const activeNavPath = (pathname: string, items: readonly NavItem[]) => {
  let best: string | undefined;
  for (const item of items) {
    const matches = item.to === "/" ? pathname === "/" : pathname === item.to || pathname.startsWith(`${item.to}/`);
    if (matches && (best === undefined || item.to.length > best.length)) best = item.to;
  }
  return best;
};
