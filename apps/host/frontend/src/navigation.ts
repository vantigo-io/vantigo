import type { ComponentType } from "react";

// The module keys this build knows. Which of them are enabled comes from the
// injected runtime config (see lib/enabled-modules.ts); which destinations
// belong to which module is declared by the app registry (apps.ts).
export const moduleKeys = [
  "accounting",
  "agents",
  "assistant",
  "calendar",
  "communications",
  "customers",
  "energy",
  "entra",
  "expenses",
  "google",
  "inventory",
  "invoices",
  "knowledge",
  "manufacturing",
  "meta",
  "orders",
  "planning",
  "products",
  "projects",
  "staff",
  "time",
] as const;
export type ModuleKey = (typeof moduleKeys)[number];

/**
 * An instance fact a destination depends on that is not a module: the AI
 * models page exists while the instance has an AI provider (Integrations
 * design T1, T13), and the Assistant app while the signed-in person has the
 * assistant (design S8). The host learns them by asking the API, not from the
 * page it was served as modules are, so they are part of the visibility
 * context.
 */
export type NavFeature = "ai" | "assistant";

export interface NavItem {
  label: string;
  to: string;
  icon: ComponentType<{ size?: number | string; stroke?: number | string }>;
  ownerOnly?: boolean;
  systemAdminOnly?: boolean;
  capability?: "authorization";
  requiredPermissions?: readonly string[];
  /**
   * Permissions the caller must hold every one of, beside any one of
   * `requiredPermissions` — for a destination whose API asks several
   * together, which `requiredPermissions` (any-of) cannot say. The quick
   * invoice is the case: its API needs invoices:create and invoices:issue
   * both. The route guard asks for them too.
   */
  requiredAllPermissions?: readonly string[];
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
  /**
   * Modules of which any one enabled shows this destination (Google Workspace
   * design G9): a host page several providers fill, as Settings › Connections.
   * Beside `module`, both must hold.
   */
  modules?: readonly ModuleKey[];
  /** The instance feature that must be present for this destination. */
  feature?: NavFeature;
  /**
   * The destination is a page the server serves beside the SPA, not a client
   * route: `to` is its path under the app's base path, it opens in a new tab,
   * and Spotlight does not offer it. The built-in documentation is the case.
   */
  external?: boolean;
  /**
   * The search the destination opens with, when it is another entry's page
   * filtered: Inventory's Receipts is its movements list with kind=receipt
   * (inventory design §13.3). The sidebar link and Spotlight carry it, and
   * the entry is the active one only while the URL carries it.
   */
  search?: Readonly<Record<string, string>>;
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

/** Whether the caller holds every one of `required` (all-of), each judged as `hasPermissions` judges one. */
export const hasAllPermissions = (permissions: string[] | undefined, required?: readonly string[]) =>
  (required ?? []).every((permission) => hasPermissions(permissions, [permission]));

export interface NavVisibilityContext {
  permissions: string[] | undefined;
  isOwner: boolean;
  canManageAuthorization: boolean;
  isSystemAdmin?: boolean;
  /** Enabled module keys; undefined while unknown (hides module destinations). */
  enabledModules?: readonly ModuleKey[];
  /** The instance features present; undefined while unknown (hides feature destinations). */
  features?: readonly NavFeature[];
}

/**
 * Filters sections to the items the caller may see and drops sections that
 * end up empty. Generic over the section type so catalogs with extra fields
 * (the account menu's required label) keep them.
 */
export const visibleNavSections = <S extends NavSection>(
  sections: readonly S[],
  {
    permissions,
    isOwner,
    canManageAuthorization,
    isSystemAdmin = false,
    enabledModules,
    features,
  }: NavVisibilityContext,
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
          (!item.modules || item.modules.some((key) => enabledModules?.includes(key) === true)) &&
          (!item.feature || features?.includes(item.feature) === true) &&
          hasPermissions(permissions, item.requiredPermissions) &&
          hasAllPermissions(permissions, item.requiredAllPermissions),
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
        assignedUserId: undefined,
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

/** An entry's identity among its section's: its path, and its search when it has one, since two may share a path. */
export const navItemKey = (item: NavItem) =>
  item.search ? `${item.to}?${new URLSearchParams(item.search).toString()}` : item.to;

/**
 * The entry the URL is on: the longest matching path. An entry with a search
 * matches only while the URL carries it, and beats a sibling on the same path
 * without one. No nav destination is "/" (the root route is a pure redirect,
 * never a nav target), so matching only needs the prefix form.
 */
export const activeNavItem = (
  pathname: string,
  items: readonly NavItem[],
  search: Readonly<Record<string, unknown>> = {},
) => {
  let best: NavItem | undefined;
  for (const item of items) {
    const matches =
      (pathname === item.to || pathname.startsWith(`${item.to}/`)) &&
      Object.entries(item.search ?? {}).every(([key, value]) => String(search[key]) === value);
    if (
      matches &&
      (best === undefined ||
        item.to.length > best.to.length ||
        (item.to.length === best.to.length && !!item.search && !best.search))
    ) {
      best = item;
    }
  }
  return best;
};

export const activeNavPath = (pathname: string, items: readonly NavItem[]) => activeNavItem(pathname, items)?.to;
