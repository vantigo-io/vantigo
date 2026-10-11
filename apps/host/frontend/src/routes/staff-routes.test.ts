import { QueryClient } from "@tanstack/react-query";
import { createMemoryHistory, createRouter, isNotFound, isRedirect } from "@tanstack/react-router";
import {
  MY_PAGE_ROUTE_PATH,
  PEOPLE_ROUTE_PATH,
  PERSON_ROUTE_PATH,
  STAFF_HOME_PATH,
  STAFF_SETTINGS_ROUTE_PATH,
  staffNavItems,
  validatePeopleSearch,
  validatePersonSearch,
} from "@vantigo/staff-ui";
import { describe, expect, it } from "vitest";
import { routeTree } from "../routeTree.gen";
import { Route as StaffRoute } from "./staff";
import { Route as StaffIndexRoute } from "./staff/index";
import { Route as StaffMeRoute } from "./staff/me";
import { Route as StaffPersonRoute } from "./staff/people.$id";
import { Route as StaffPeopleRoute } from "./staff/people.index";

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

  it("sits under the staff layout, which carries the app and its module gate", () => {
    expect(matchedRouteIds(PEOPLE_ROUTE_PATH)).toContain(STAFF_HOME_PATH);
    expect(StaffRoute.options.staticData).toEqual({ app: "staff" });
  });

  // `/staff` is a pure redirect: My page is the one destination every Staff
  // user is offered (`staff:access`); People and the settings are a click away
  // for those who hold more.
  it("sends /staff to My page", () => {
    expect(StaffIndexRoute.options.component).toBeUndefined();
    let thrown: unknown;
    try {
      (StaffIndexRoute.options.beforeLoad as () => void)();
    } catch (error) {
      thrown = error;
    }
    expect(isRedirect(thrown)).toBe(true);
    expect((thrown as { options?: { to?: string } }).options?.to).toBe(MY_PAGE_ROUTE_PATH);
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
    expect(StaffMeRoute.options.validateSearch).toBe(validatePersonSearch);
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
