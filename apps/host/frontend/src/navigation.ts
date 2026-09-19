import type { ComponentType } from "react";

// The module keys this build knows. Which of them are enabled comes from the
// injected runtime config (see lib/enabled-modules.ts); which destinations
// belong to which module is declared by the app registry (apps.ts).
export const moduleKeys = ["communications", "customers", "energy", "products", "projects", "time"] as const;
export type ModuleKey = (typeof moduleKeys)[number];

export interface NavItem {
  label: string;
  to: string;
  icon: ComponentType<{ size?: number | string; stroke?: number | string }>;
  ownerOnly?: boolean;
  systemAdminOnly?: boolean;
  capability?: "authorization";
  requiredPermissions?: readonly string[];
  /**
   * What the route guard demands on this destination's URL prefix, when that
   * is less than the sidebar entry asks for. The Time app's approval queue is
   * the case it exists for: the entry is offered to the permission-holding
   * approvers, but a project manager approves through their role and reaches
   * the same page from the dashboard or by pasting the URL, so the guard must
   * not turn that into an access-denied page. Omitted = the guard uses
   * `requiredPermissions`, which is what every other destination wants.
   */
  guardPermissions?: readonly string[];
  /** The module that must be enabled for this destination. */
  module?: ModuleKey;
  /** Search defaults used when Spotlight opens this destination. */
  searchStrategy?: "customer-list" | "inbox-list" | "products-list" | "energy-list" | "projects-list" | "time-week";
}
export interface NavSection {
  /** Optional section heading; unlabeled sections render items only. */
  label?: string;
  items: readonly NavItem[];
}

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

/**
 * Filters sections to the items the caller may see and drops sections that
 * end up empty. Generic over the section type so catalogs with extra fields
 * (the account menu's required label) keep them.
 */
export const visibleNavSections = <S extends NavSection>(
  sections: readonly S[],
  { permissions, isOwner, canManageAuthorization, isSystemAdmin = false, enabledModules }: NavVisibilityContext,
): S[] =>
  sections
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
    case "projects-list":
      return { page: 1, search: "", status: "", mine: false };
    // My week has no default week: without one the page stands on the current
    // week, so the entry always opens today's week rather than whichever one
    // the caller last looked at.
    case "time-week":
      return { week: undefined };
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
