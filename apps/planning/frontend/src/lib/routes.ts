/**
 * The paths the Planning app lives at. The host owns the routes, but the
 * package's own pages link to them — a board row to the person's timeline, a
 * project row to the project's plan — so each path is written once, here, and
 * the host's route files and this package's test route tree are both built
 * from it. A link that does not match a mounted route is a runtime error in
 * TanStack Router, and this is what keeps them from drifting apart.
 */
export const BOARD_ROUTE_PATH = "/planning";
export const PROJECT_PLAN_ROUTE_PATH = "/planning/projects/$projectId";
export const PERSON_TIMELINE_ROUTE_PATH = "/planning/people/$personId";
export const REQUESTS_ROUTE_PATH = "/planning/requests";
export const INTAKE_ROUTE_PATH = "/planning/intake";
/** The planners' list of open wishes (design S21), `planning:manage`'s. */
export const WISHES_ROUTE_PATH = "/planning/wishes";
export const UNBOOKED_ROUTE_PATH = "/planning/unbooked";
export const MY_PLANNING_ROUTE_PATH = "/planning/me";
export const PLANNING_SETTINGS_ROUTE_PATH = "/planning/settings";

/** What `navigate` and `Link` take to reach one project's plan. */
export const projectPlanLinkOptions = (projectId: number) => ({
  to: PROJECT_PLAN_ROUTE_PATH,
  params: { projectId: String(projectId) },
});

/** What `navigate` and `Link` take to reach one person's timeline. */
export const personTimelineLinkOptions = (personId: number) => ({
  to: PERSON_TIMELINE_ROUTE_PATH,
  params: { personId: String(personId) },
});
