import { BOARD_ROUTE_PATH, MY_PLANNING_ROUTE_PATH, PLANNING_SETTINGS_ROUTE_PATH, UNBOOKED_ROUTE_PATH } from "./routes";

/**
 * One destination of the Planning app's sidebar (design §7), as the host
 * registers it. The host owns the sidebar, its icons and its labels; this is
 * the part that is the module's to say — where each destination is, which
 * permission offers it, and the label in this package's catalog — so the
 * host's entry and this package's own gate cannot disagree. The project's
 * plan and the person's timeline are no sidebar entries: the board leads to
 * them.
 */
export interface PlanningNavItem {
  key: "board" | "unbooked" | "myPlanning" | "settings";
  /** A key in the `planning` catalog. */
  label: string;
  to: string;
  /** Any one of these offers the destination in the sidebar. */
  requiredPermissions: readonly string[];
}

export const planningNavItems: readonly PlanningNavItem[] = [
  { key: "board", label: "navBoard", to: BOARD_ROUTE_PATH, requiredPermissions: ["planning:view"] },
  { key: "unbooked", label: "navUnbooked", to: UNBOOKED_ROUTE_PATH, requiredPermissions: ["planning:view"] },
  { key: "myPlanning", label: "navMyPlanning", to: MY_PLANNING_ROUTE_PATH, requiredPermissions: ["planning:access"] },
  { key: "settings", label: "navSettings", to: PLANNING_SETTINGS_ROUTE_PATH, requiredPermissions: ["planning:manage"] },
];

/** The destinations a caller holding these permissions is offered; `*` is the Owner's wildcard. */
export const visiblePlanningNavItems = (permissions: readonly string[] | undefined): PlanningNavItem[] =>
  planningNavItems.filter(
    (item) =>
      permissions?.includes("*") === true ||
      item.requiredPermissions.some((permission) => permissions?.includes(permission)),
  );
