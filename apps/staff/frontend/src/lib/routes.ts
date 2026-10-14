/**
 * The paths the Staff app lives at. The host owns the routes (Task 6), but
 * the package's own pages link to them — a People row to the person, New
 * person to the person it created — so each path is written once, here, and
 * the host's route files and this package's test route tree are both built
 * from it. A link that does not match a mounted route is a runtime error in
 * TanStack Router, and this is what keeps them from drifting apart.
 */
export const STAFF_HOME_PATH = "/staff";
export const MY_PAGE_ROUTE_PATH = "/staff/me";
export const PEOPLE_ROUTE_PATH = "/staff/people";
export const PERSON_ROUTE_PATH = "/staff/people/$id";
export const STAFF_SETTINGS_ROUTE_PATH = "/staff/settings";
/** The absence calendar and the approvals inbox; the dashboard's approval items open it. */
export const ABSENCE_ROUTE_PATH = "/staff/absence";

/** What `navigate` and `Link` take to reach one person. */
export const personLinkOptions = (personId: number) => ({
  to: PERSON_ROUTE_PATH,
  params: { id: String(personId) },
});
