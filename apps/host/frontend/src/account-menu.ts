import {
  IconBrandGoogle,
  IconBrandWindows,
  IconBuilding,
  IconBuildingBank,
  IconBuildingSkyscraper,
  IconHelp,
  IconSettings,
  IconShieldCheck,
  IconTruckDelivery,
} from "@tabler/icons-react";
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
 * and invitations remain in-page tabs of the workspace area. Help opens the
 * documentation the server embeds under /docs, in the reader's language.
 */
export const accountMenuSections: readonly AccountMenuSection[] = [
  {
    label: "navigation.accountSection",
    items: [
      { label: "navigation.settings", to: "/settings", icon: IconSettings },
      { label: "navigation.help", to: "/docs", icon: IconHelp, external: true },
    ],
  },
  {
    label: "navigation.workspaceSection",
    items: [
      { label: "navigation.workspaceAdmin", to: "/workspace/overview", icon: IconBuilding, ownerOnly: true },
      { label: "navigation.rolesAccess", to: "/workspace/roles", icon: IconShieldCheck, capability: "authorization" },
      // Under Integrations in Workspace admin; here so a holder of entra:manage, google:manage,
      // orders:carriers-manage or invoices:manage who is not an Owner can reach the page, as an
      // authorization manager reaches Roles & access. Spotlight reads this list too.
      {
        label: "navigation.entra",
        to: "/workspace/entra",
        icon: IconBrandWindows,
        module: "entra",
        requiredPermissions: ["entra:manage"],
      },
      {
        label: "navigation.google",
        to: "/workspace/google",
        icon: IconBrandGoogle,
        module: "google",
        requiredPermissions: ["google:manage"],
      },
      {
        label: "navigation.shipping",
        to: "/workspace/shipping",
        icon: IconTruckDelivery,
        module: "orders",
        requiredPermissions: ["orders:carriers-manage"],
      },
      {
        label: "navigation.bank",
        to: "/workspace/bank",
        icon: IconBuildingBank,
        module: "invoices",
        requiredPermissions: ["invoices:manage"],
        requiredAllPermissions: ["invoices:access"],
      },
    ],
  },
  {
    label: "navigation.systemSection",
    items: [{ label: "navigation.systemAdmin", to: "/admin", icon: IconBuildingSkyscraper, systemAdminOnly: true }],
  },
];
