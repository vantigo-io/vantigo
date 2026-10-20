import { QueryClient } from "@tanstack/react-query";
import { createMemoryHistory, createRouter, isNotFound, isRedirect } from "@tanstack/react-router";
import {
  ABSENCE_ROUTE_PATH,
  GROUP_ROUTE_PATH,
  GROUPS_ROUTE_PATH,
  MY_PAGE_ROUTE_PATH,
  PEOPLE_ROUTE_PATH,
  PERSON_ROUTE_PATH,
  STAFF_HOME_PATH,
  STAFF_SETTINGS_ROUTE_PATH,
  staffNavItems,
  validateAbsenceSearch,
  validateMyPageSearch,
  validatePeopleSearch,
  validatePersonSearch,
} from "@vantigo/staff-ui";
import { describe, expect, it, vi } from "vitest";
import { routeTree } from "../routeTree.gen";
import { attentionHref } from "./dashboard";
import { Route as StaffRoute } from "./staff";
import { Route as StaffAbsenceRoute } from "./staff/absence";
import { Route as StaffGroupRoute } from "./staff/groups.$id";
import { Route as StaffIndexRoute } from "./staff/index";
import { Route as StaffMeRoute } from "./staff/me";
import { Route as StaffPersonRoute } from "./staff/people.$id";
import { Route as StaffPeopleRoute } from "./staff/people.index";

const { getAuthorizationMe } = vi.hoisted(() => ({ getAuthorizationMe: vi.fn() }));
vi.mock("../api/authorization", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../api/authorization")>()),
  getAuthorizationMe,
}));

/** Drives `/staff/`'s `beforeLoad` with a fresh query client; returns what it throws. */
const indexRedirect = async () => {
  const beforeLoad = StaffIndexRoute.options.beforeLoad as (context: unknown) => Promise<void>;
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  try {
    await beforeLoad({ context: { queryClient } });
  } catch (error) {
    return error as { options?: { to?: string; search?: unknown } };
  }
  return undefined;
};

/**
 * The route ids the generated tree matches for a path, through the real
 * router, with an index route's trailing slash dropped: People is
 * `people.index.tsx` so that a person's page is its sibling, not its child.
 */
const matchedRouteIds = (pathname: string) => {
  const router = createRouter({
    routeTree,
    context: { queryClient: new QueryClient() },
    history: createMemoryHistory({ initialEntries: [pathname] }),
  });
  return router.matchRoutes(pathname, {}).map((match) => match.routeId.replace(/(.)\/$/, "$1"));
};

/**
 * The Staff package writes its paths once (`lib/routes.ts`) and links to them
 * from its own pages; the host mounts them. A link that matches no mounted
 * route is a runtime error, so these pin that every path the package names —
 * its sidebar's and the person page's — is one the generated tree serves.
 */
describe("the staff routes", () => {
  it("mounts every path the package links to", () => {
    expect(matchedRouteIds(MY_PAGE_ROUTE_PATH)).toContain(MY_PAGE_ROUTE_PATH);
    expect(matchedRouteIds(PEOPLE_ROUTE_PATH)).toContain(PEOPLE_ROUTE_PATH);
    expect(matchedRouteIds(STAFF_SETTINGS_ROUTE_PATH)).toContain(STAFF_SETTINGS_ROUTE_PATH);
    expect(matchedRouteIds("/staff/people/12")).toContain(PERSON_ROUTE_PATH);
    expect(matchedRouteIds("/staff/people/12")).not.toContain(PEOPLE_ROUTE_PATH);
    for (const item of staffNavItems) expect(matchedRouteIds(item.to)).toContain(item.to);
  });

  // The dashboard's approval items open the Absence page, which a manager by
  // the terms reaches by the URL though the sidebar cannot offer it them.
  it("mounts the Absence page the dashboard's approval items link to, with the package's validator", () => {
    expect(ABSENCE_ROUTE_PATH).toBe("/staff/absence");
    expect(matchedRouteIds(ABSENCE_ROUTE_PATH)).toContain(ABSENCE_ROUTE_PATH);
    for (const type of ["absenceAwaitingDecision", "hrOnlyAwaitingDecision"])
      expect(attentionHref({ module: "staff", type, entityId: "approvals" })).toBe(ABSENCE_ROUTE_PATH);
    expect(StaffAbsenceRoute.options.validateSearch).toBe(validateAbsenceSearch);
  });

  // The groups (Entra delivery D): the list and one group are siblings, as
  // People and a person are, and the dashboard's groupSyncFailed item opens one.
  it("mounts the groups and one group, the page the dashboard's group item links to", () => {
    expect(matchedRouteIds(GROUPS_ROUTE_PATH)).toContain(GROUPS_ROUTE_PATH);
    expect(matchedRouteIds("/staff/groups/1001")).toContain(GROUP_ROUTE_PATH);
    expect(matchedRouteIds("/staff/groups/1001")).not.toContain(GROUPS_ROUTE_PATH);
    expect(attentionHref({ module: "staff", type: "groupSyncFailed", entityId: "1001" })).toBe("/staff/groups/1001");
    expect(matchedRouteIds(GROUPS_ROUTE_PATH)).toContain(STAFF_HOME_PATH);
  });

  it("refuses a group id that is not a positive whole number", () => {
    const guard = StaffGroupRoute.options.beforeLoad as (context: { params: { id: string } }) => void;
    expect(() => guard({ params: { id: "1001" } })).not.toThrow();
    for (const id of ["0", "-3", "abc", "1.5", ""]) {
      let thrown: unknown;
      try {
        guard({ params: { id } });
      } catch (error) {
        thrown = error;
      }
      expect(isNotFound(thrown), `id ${JSON.stringify(id)}`).toBe(true);
    }
  });

  it("sits under the staff layout, which carries the app and its module gate", () => {
    expect(matchedRouteIds(PEOPLE_ROUTE_PATH)).toContain(STAFF_HOME_PATH);
    expect(StaffRoute.options.staticData).toEqual({ app: "staff" });
  });

  // `/staff` is a pure redirect: People for whoever the sidebar offers it
  // (staff:view or staff:manage, beside the app's own staff:access), My page
  // for everyone else — and for anyone whose permissions could not be read.
  it.each([
    [["staff:access", "staff:view"], PEOPLE_ROUTE_PATH],
    [["staff:access", "staff:manage"], PEOPLE_ROUTE_PATH],
    [["*"], PEOPLE_ROUTE_PATH],
    [["staff:access"], MY_PAGE_ROUTE_PATH],
    [["staff:access", "staff:approve", "staff:personal-data"], MY_PAGE_ROUTE_PATH],
    [["staff:view", "staff:manage"], MY_PAGE_ROUTE_PATH],
  ])("sends %j from /staff to %s", async (permissions, to) => {
    getAuthorizationMe.mockResolvedValue({ permissions });
    expect(StaffIndexRoute.options.component).toBeUndefined();

    const thrown = await indexRedirect();

    expect(isRedirect(thrown)).toBe(true);
    expect(thrown?.options?.to).toBe(to);
    if (to === PEOPLE_ROUTE_PATH) expect(thrown?.options?.search).toEqual(validatePeopleSearch({}));
  });

  it("sends /staff to My page when the permissions cannot be read", async () => {
    getAuthorizationMe.mockRejectedValue(new Error("offline"));

    const thrown = await indexRedirect();

    expect(isRedirect(thrown)).toBe(true);
    expect(thrown?.options?.to).toBe(MY_PAGE_ROUTE_PATH);
  });

  // The validators are the package's own, so the route and the package's
  // tests read a URL the same way; these pin that the route files hand the
  // URL to them rather than reinventing a parser.
  it("validates People's search with the package's validator, the create seam included", () => {
    const validate = StaffPeopleRoute.options.validateSearch as (search: Record<string, unknown>) => unknown;
    expect(StaffPeopleRoute.options.validateSearch).toBe(validatePeopleSearch);
    expect(validate({})).toEqual({
      q: undefined,
      unitId: undefined,
      departmentId: undefined,
      locationId: undefined,
      status: undefined,
      page: 1,
      create: undefined,
    });
    expect(validate({ create: "person" })).toMatchObject({ create: "person" });
  });

  it("validates the person page's and My page's tab with the package's validator", () => {
    expect(StaffPersonRoute.options.validateSearch).toBe(validatePersonSearch);
    // My page has its own tabs besides a person's: Absence, and My team.
    expect(StaffMeRoute.options.validateSearch).toBe(validateMyPageSearch);
    expect(validateMyPageSearch({ tab: "absence" })).toEqual({ tab: "absence" });
    expect(validateMyPageSearch({ tab: "team" })).toEqual({ tab: "team" });
    expect(validatePersonSearch({ tab: "team" })).toEqual({ tab: undefined });
    // The spotlight's Request absence lands on My page with the sheet open.
    expect(validateMyPageSearch({ tab: "absence", create: "absence" })).toEqual({ tab: "absence", create: "absence" });
  });

  // A person's id is a positive whole number: anything else in the path never
  // reaches the API as `GET /people/NaN`, it is the layout's not-found.
  it("refuses a person id that is not a positive whole number", () => {
    const guard = StaffPersonRoute.options.beforeLoad as (context: { params: { id: string } }) => void;
    expect(() => guard({ params: { id: "12" } })).not.toThrow();
    for (const id of ["0", "-3", "1e3", "0x10", " 12", "12abc", "1.5", "", "99999999999999999999"]) {
      let thrown: unknown;
      try {
        guard({ params: { id } });
      } catch (error) {
        thrown = error;
      }
      expect(isNotFound(thrown), `id ${JSON.stringify(id)}`).toBe(true);
    }
  });
});
