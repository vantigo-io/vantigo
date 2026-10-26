import { QueryClient } from "@tanstack/react-query";
import { createMemoryHistory, createRouter, isNotFound, isRedirect } from "@tanstack/react-router";
import {
  BOARD_ROUTE_PATH,
  INTAKE_ROUTE_PATH,
  MY_PLANNING_ROUTE_PATH,
  PERSON_TIMELINE_ROUTE_PATH,
  PLANNING_SETTINGS_ROUTE_PATH,
  PROJECT_PLAN_ROUTE_PATH,
  planningNavItems,
  REQUESTS_ROUTE_PATH,
  UNBOOKED_ROUTE_PATH,
  validateBoardSearch,
  validateRequestsSearch,
  validateWindowSearch,
} from "@vantigo/planning-ui";
import { describe, expect, it, vi } from "vitest";
import { routeTree } from "../routeTree.gen";
import { attentionHref } from "./dashboard";
import { Route as PlanningRoute } from "./planning";
import { Route as PlanningIndexRoute } from "./planning/index";
import { Route as PlanningIntakeRoute } from "./planning/intake";
import { Route as PlanningPersonRoute } from "./planning/people.$personId";
import { Route as PlanningProjectRoute } from "./planning/projects.$projectId";
import { Route as PlanningRequestsRoute } from "./planning/requests";

const { getAuthorizationMe } = vi.hoisted(() => ({ getAuthorizationMe: vi.fn() }));
vi.mock("../api/authorization", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../api/authorization")>()),
  getAuthorizationMe,
}));

/** Drives a route's `beforeLoad` with a fresh query client; returns what it throws. */
const runBeforeLoad = async (route: { options: { beforeLoad?: unknown } }, params: Record<string, string> = {}) => {
  const beforeLoad = route.options.beforeLoad as (context: unknown) => Promise<void> | void;
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  try {
    await beforeLoad({ context: { queryClient }, params });
  } catch (error) {
    return error as { options?: { to?: string } };
  }
  return undefined;
};

/** The route ids the generated tree matches for a path, through the real router, trailing slashes dropped. */
const matchedRouteIds = (pathname: string) => {
  const router = createRouter({
    routeTree,
    context: { queryClient: new QueryClient() },
    history: createMemoryHistory({ initialEntries: [pathname] }),
  });
  return router.matchRoutes(pathname, {}).map((match) => match.routeId.replace(/(.)\/$/, "$1"));
};

/**
 * The Planning package writes its paths once (`lib/routes.ts`) and links to
 * them from its own pages; the host mounts them. A link that matches no
 * mounted route is a runtime error, so these pin that every path the package
 * names is one the generated tree serves.
 */
describe("the planning routes", () => {
  it("mounts every path the package links to", () => {
    expect(matchedRouteIds(BOARD_ROUTE_PATH)).toContain(BOARD_ROUTE_PATH);
    expect(matchedRouteIds(REQUESTS_ROUTE_PATH)).toContain(REQUESTS_ROUTE_PATH);
    expect(matchedRouteIds(INTAKE_ROUTE_PATH)).toContain(INTAKE_ROUTE_PATH);
    expect(matchedRouteIds(UNBOOKED_ROUTE_PATH)).toContain(UNBOOKED_ROUTE_PATH);
    expect(matchedRouteIds(MY_PLANNING_ROUTE_PATH)).toContain(MY_PLANNING_ROUTE_PATH);
    expect(matchedRouteIds(PLANNING_SETTINGS_ROUTE_PATH)).toContain(PLANNING_SETTINGS_ROUTE_PATH);
    expect(matchedRouteIds("/planning/projects/31")).toContain(PROJECT_PLAN_ROUTE_PATH);
    expect(matchedRouteIds("/planning/people/101")).toContain(PERSON_TIMELINE_ROUTE_PATH);
    for (const item of planningNavItems) expect(matchedRouteIds(item.to)).toContain(item.to);
  });

  it("sits under the planning layout, which carries the app and its module gate", () => {
    expect(matchedRouteIds(UNBOOKED_ROUTE_PATH)).toContain("/planning");
    expect(matchedRouteIds(REQUESTS_ROUTE_PATH)).toContain("/planning");
    expect(matchedRouteIds(INTAKE_ROUTE_PATH)).toContain("/planning");
    expect(matchedRouteIds("/planning/people/101")).toContain("/planning");
    expect(PlanningRoute.options.staticData).toEqual({ app: "planning" });
  });

  // The validators are the package's own, so the route and the package's
  // tests read a URL the same way.
  it("validates the board's search, the windows' and the requests' with the package's validators", () => {
    expect(PlanningIndexRoute.options.validateSearch).toBe(validateBoardSearch);
    expect(PlanningRequestsRoute.options.validateSearch).toBe(validateRequestsSearch);
    expect(PlanningProjectRoute.options.validateSearch).toBe(validateWindowSearch);
    expect(PlanningPersonRoute.options.validateSearch).toBe(validateWindowSearch);
    // The spotlight's New allocation lands on the board with the sheet open.
    expect(validateBoardSearch({ create: "allocation" })).toEqual({ create: "allocation" });
  });

  // An id is a positive whole number: anything else never reaches the API as
  // `GET /…/NaN`, it is the layout's not-found.
  it.each([
    ["project", PlanningProjectRoute, "projectId"],
    ["person", PlanningPersonRoute, "personId"],
  ] as const)("refuses a %s id that is not a positive whole number", async (_, route, param) => {
    getAuthorizationMe.mockResolvedValue({ permissions: ["*"] });
    expect(await runBeforeLoad(route, { [param]: "12" })).toBeUndefined();
    for (const id of ["0", "-3", "1e3", "0x10", " 12", "12abc", "1.5", "", "99999999999999999999"]) {
      expect(isNotFound(await runBeforeLoad(route, { [param]: id })), `id ${JSON.stringify(id)}`).toBe(true);
    }
  });

  // The board, a project's plan, the requests and the intake are
  // planning:view's; their URLs are under the prefix the guard asks only
  // planning:access of, so the routes send anyone else — and anyone whose
  // permissions could not be read — to My planning. A person's own timeline
  // stays reachable for planning:access.
  it.each([
    [["planning:access", "planning:view"], undefined],
    [["*"], undefined],
    [["planning:access"], MY_PLANNING_ROUTE_PATH],
    [["planning:access", "planning:manage"], MY_PLANNING_ROUTE_PATH],
  ])(
    "lets %j onto the board, a project's plan, the requests and the intake, or sends them to %s",
    async (permissions, to) => {
      getAuthorizationMe.mockResolvedValue({ permissions });
      for (const [route, params] of [
        [PlanningIndexRoute, {}],
        [PlanningProjectRoute, { projectId: "31" }],
        [PlanningRequestsRoute, {}],
        [PlanningIntakeRoute, {}],
      ] as const) {
        const thrown = await runBeforeLoad(route, params);
        if (to === undefined) expect(thrown).toBeUndefined();
        else {
          expect(isRedirect(thrown)).toBe(true);
          expect(thrown?.options?.to).toBe(to);
        }
      }
      expect(await runBeforeLoad(PlanningPersonRoute, { personId: "101" })).toBeUndefined();
    },
  );

  it("sends the board to My planning when the permissions cannot be read", async () => {
    getAuthorizationMe.mockRejectedValue(new Error("offline"));
    const thrown = await runBeforeLoad(PlanningIndexRoute);
    expect(isRedirect(thrown)).toBe(true);
    expect(thrown?.options?.to).toBe(MY_PLANNING_ROUTE_PATH);
  });

  // The dashboard's items open the screen that answers them (planning design S22).
  it("links each attention item to a mounted page", () => {
    expect(attentionHref({ module: "planning", type: "unbooked", entityId: "101" })).toBe(UNBOOKED_ROUTE_PATH);
    expect(attentionHref({ module: "planning", type: "overbooked", entityId: "101" })).toBe("/planning/people/101");
    expect(attentionHref({ module: "planning", type: "certificatesExpiring", entityId: "101" })).toBe(
      "/planning/people/101",
    );
    expect(attentionHref({ module: "planning", type: "endingWithoutFollowOn", entityId: "7001" })).toBe(
      BOARD_ROUTE_PATH,
    );
    expect(attentionHref({ module: "planning", type: "requestsOpen", entityId: "41" })).toBe(
      `${REQUESTS_ROUTE_PATH}?status=open`,
    );
    expect(matchedRouteIds("/planning/people/101")).toContain(PERSON_TIMELINE_ROUTE_PATH);
  });
});
