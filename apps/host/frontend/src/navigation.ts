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

export interface NavItem {
  label: string;
  to: string;
  icon: ComponentType<{ size?: number | string; stroke?: number | string }>;
  ownerOnly?: boolean;
  systemAdminOnly?: boolean;
  capability?: "authorization";
  requiredPermissions?: readonly string[];
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
        icon: IconUsers,
        requiredPermissions: ["customers:view"],
        searchStrategy: "customer-list",
        tenantScoped: true,
      },
      {
        label: "navigation.contacts",
        to: "/contacts",
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
        icon: IconInbox,
        requiredPermissions: ["communications:conversations-view"],
        searchStrategy: "inbox-list",
        tenantScoped: true,
      },
      {
        label: "navigation.channels",
        to: "/communications/channels",
        icon: IconMailbox,
        requiredPermissions: ["communications:channels-manage"],
        tenantScoped: true,
      },
      {
        label: "navigation.suppressions",
        to: "/communications/suppressions",
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
  required.every((permission) => permissions?.includes(permission));
export const visibleNavSections = (
  permissions: string[] | undefined,
  isOwner: boolean,
  canManageAuthorization: boolean,
  tenantSlug?: string,
  isSystemAdmin = false,
) =>
  navSectionsForTenant(tenantSlug)
    .map((section) => ({
      ...section,
      items: section.items.filter(
        (item) =>
          (!item.ownerOnly || isOwner) &&
          (!item.systemAdminOnly || isSystemAdmin) &&
          (!item.capability || canManageAuthorization) &&
          hasPermissions(permissions, item.requiredPermissions),
      ),
    }))
    .filter((section) => section.items.length > 0);

/** Select the first accessible business destination for the authenticated landing route. */
export const firstAuthorizedIntegratedAppDestination = (
  permissions: string[] | undefined,
  isOwner: boolean,
  canManageAuthorization: boolean,
  tenantSlug?: string,
) =>
  visibleNavSections(permissions, isOwner, canManageAuthorization, tenantSlug)
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
