import {
  IconAddressBook,
  IconBolt,
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
  capability?: "authorization";
  requiredPermissions?: readonly string[];
  /** Search defaults used when Spotlight opens this destination. */
  searchStrategy?: "customer-list" | "messages-list" | "products-list" | "energy-list";
}
export interface NavSection {
  label: string;
  items: readonly NavItem[];
  placement?: "lower";
}

export const navSections: readonly NavSection[] = [
  {
    label: "Customer workspace",
    items: [
      {
        label: "Customers",
        to: "/customers",
        icon: IconUsers,
        requiredPermissions: ["customers:view"],
        searchStrategy: "customer-list",
      },
      {
        label: "Contacts",
        to: "/contacts",
        icon: IconAddressBook,
        requiredPermissions: ["customers:contacts-view", "customers:associations-view"],
        searchStrategy: "customer-list",
      },
    ],
  },
  {
    label: "Communications",
    items: [
      {
        label: "Messages",
        to: "/messages",
        icon: IconInbox,
        requiredPermissions: ["communications:messages-view"],
        searchStrategy: "messages-list",
      },
      {
        label: "Mailboxes",
        to: "/communications/mailboxes",
        icon: IconMailbox,
        requiredPermissions: ["communications:mailboxes-view"],
      },
      {
        label: "Suppressions",
        to: "/communications/suppressions",
        icon: IconMailOff,
        requiredPermissions: ["communications:suppressions-view"],
      },
    ],
  },
  {
    label: "Catalog",
    items: [
      {
        label: "Products",
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
        label: "Categories",
        to: "/products/categories",
        icon: IconCategory,
        requiredPermissions: ["products:categories-view"],
      },
    ],
  },
  {
    label: "Energy",
    items: [
      {
        label: "Metering points",
        to: "/energy/metering-points",
        icon: IconBolt,
        requiredPermissions: ["energy:metering-points-view", "energy:meters-view"],
        searchStrategy: "energy-list",
      },
    ],
  },
  {
    label: "Settings & administration",
    placement: "lower",
    items: [
      { label: "Settings", to: "/settings", icon: IconSettings },
      { label: "Admin dashboard", to: "/admin/dashboard", icon: IconLayoutDashboard, ownerOnly: true },
      { label: "Roles & access", to: "/admin/roles", icon: IconShieldCheck, capability: "authorization" },
    ],
  },
];

export const hasPermissions = (permissions: string[] | undefined, required?: readonly string[]) =>
  !required?.length ||
  permissions?.includes("*") === true ||
  required.every((permission) => permissions?.includes(permission));
export const visibleNavSections = (
  permissions: string[] | undefined,
  isOwner: boolean,
  canManageAuthorization: boolean,
) =>
  navSections
    .map((section) => ({
      ...section,
      items: section.items.filter(
        (item) =>
          (!item.ownerOnly || isOwner) &&
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
) =>
  visibleNavSections(permissions, isOwner, canManageAuthorization)
    .filter((section) => section.placement !== "lower")
    .flatMap((section) => section.items)[0]?.to;

export const navSearchFor = (strategy: NavItem["searchStrategy"]) => {
  switch (strategy) {
    case "customer-list":
      return { page: 1, search: "" };
    case "messages-list":
      return { page: 1, archived: undefined };
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
