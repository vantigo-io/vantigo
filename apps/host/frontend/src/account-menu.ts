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
    items: [{ label: "navigation.systemAdmin", to: "/admin", icon: IconBuildingSkyscraper, systemAdminOnly: true }],
  },
];
