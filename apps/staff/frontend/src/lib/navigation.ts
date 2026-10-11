import { MY_PAGE_ROUTE_PATH, PEOPLE_ROUTE_PATH, STAFF_SETTINGS_ROUTE_PATH } from "./routes";

/**
 * One destination of the Staff app's sidebar (design S28), as the host
 * registers it. The host owns the sidebar and its icons; this is the part
 * that is the module's to say — where each destination is, which permission
 * offers it, and the label in this package's catalog — so the host's entry
 * and this package's own gate cannot disagree.
 */
export interface StaffNavItem {
  key: "myPage" | "people" | "settings";
  /** A key in the `staff` catalog. */
  label: string;
  to: string;
  /** Any one of these offers the destination in the sidebar. */
  requiredPermissions: readonly string[];
  /**
   * What the route guard demands on the URL, when that is less than the
   * sidebar asks for: People is offered to `staff:view`, but an HR role
   * holding `staff:manage` without it still lists people (the server answers
   * it, design reading 15), so the page itself is guarded by `staff:access`.
   */
  guardPermissions?: readonly string[];
}

export const staffNavItems: readonly StaffNavItem[] = [
  { key: "myPage", label: "navMyPage", to: MY_PAGE_ROUTE_PATH, requiredPermissions: ["staff:access"] },
  {
    key: "people",
    label: "navPeople",
    to: PEOPLE_ROUTE_PATH,
    requiredPermissions: ["staff:view"],
    guardPermissions: ["staff:access"],
  },
  { key: "settings", label: "navSettings", to: STAFF_SETTINGS_ROUTE_PATH, requiredPermissions: ["staff:manage"] },
];

/** The destinations a caller holding these permissions is offered; `*` is the Owner's wildcard. */
export const visibleStaffNavItems = (permissions: readonly string[] | undefined): StaffNavItem[] =>
  staffNavItems.filter(
    (item) =>
      permissions?.includes("*") === true ||
      item.requiredPermissions.some((permission) => permissions?.includes(permission)),
  );
