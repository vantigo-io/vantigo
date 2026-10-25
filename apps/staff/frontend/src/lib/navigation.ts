import {
  ABSENCE_ROUTE_PATH,
  COMPETENCE_ROUTE_PATH,
  GROUPS_ROUTE_PATH,
  MY_PAGE_ROUTE_PATH,
  PEOPLE_ROUTE_PATH,
  STAFF_SETTINGS_ROUTE_PATH,
} from "./routes";

/**
 * One destination of the Staff app's sidebar (design S28), as the host
 * registers it. The host owns the sidebar and its icons; this is the part
 * that is the module's to say — where each destination is, which permission
 * offers it, and the label in this package's catalog — so the host's entry
 * and this package's own gate cannot disagree.
 */
export interface StaffNavItem {
  key: "myPage" | "people" | "absence" | "competence" | "groups" | "settings";
  /** A key in the `staff` catalog. */
  label: string;
  to: string;
  /** Any one of these offers the destination in the sidebar. */
  requiredPermissions: readonly string[];
  /**
   * What the route guard demands on the URL, when that is less than the
   * sidebar asks for: People is offered to `staff:view` and to `staff:manage`
   * (an HR role without the directory still lists, plan reading 15), and the
   * page itself is guarded by `staff:access`, the server judging the rest.
   */
  guardPermissions?: readonly string[];
}

export const staffNavItems: readonly StaffNavItem[] = [
  { key: "myPage", label: "navMyPage", to: MY_PAGE_ROUTE_PATH, requiredPermissions: ["staff:access"] },
  {
    key: "people",
    label: "navPeople",
    to: PEOPLE_ROUTE_PATH,
    // Any of (the host's `hasPermissions`): the directory, or HR without it.
    requiredPermissions: ["staff:view", "staff:manage"],
    guardPermissions: ["staff:access"],
  },
  {
    key: "absence",
    label: "navAbsence",
    to: ABSENCE_ROUTE_PATH,
    // Any of: the calendar's readers (the directory, HR) and the deciders by
    // right — the Approver, HR, and staff:personal-data, which alone decides
    // the HR-only types (plan reading 33). A manager by the terms is no
    // permission the sidebar can see: My team decides for them, and the
    // URL — the dashboard's link — is guarded by the app alone.
    requiredPermissions: ["staff:view", "staff:approve", "staff:manage", "staff:personal-data"],
    guardPermissions: ["staff:access"],
  },
  // The matrix is the directory with competences (design S14): staff:view's
  // and staff:manage's, the URL guarded the same — the interests are HR's tab.
  {
    key: "competence",
    label: "navCompetence",
    to: COMPETENCE_ROUTE_PATH,
    requiredPermissions: ["staff:view", "staff:manage"],
  },
  // The groups are staff:manage's alone, the list and every group (Entra design D9).
  { key: "groups", label: "navGroups", to: GROUPS_ROUTE_PATH, requiredPermissions: ["staff:manage"] },
  { key: "settings", label: "navSettings", to: STAFF_SETTINGS_ROUTE_PATH, requiredPermissions: ["staff:manage"] },
];

/** The destinations a caller holding these permissions is offered; `*` is the Owner's wildcard. */
export const visibleStaffNavItems = (permissions: readonly string[] | undefined): StaffNavItem[] =>
  staffNavItems.filter(
    (item) =>
      permissions?.includes("*") === true ||
      item.requiredPermissions.some((permission) => permissions?.includes(permission)),
  );
