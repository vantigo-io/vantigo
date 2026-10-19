import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  Outlet,
  type RouteComponent,
  RouterProvider,
} from "@tanstack/react-router";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { stubFetch } from "../test/fetch";
import { Route as DashboardRoute } from "./dashboard";
import "../i18n";

const json = (body: unknown) =>
  new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } });

/**
 * The dashboard with only the modules named enabled and the permissions named
 * held. Every other module is off, so the page asks for no stats at all and
 * the fetch stub need answer only the authorization read and the calendar.
 */
const renderDashboard = async (modules: string[], permissions: string[]) => {
  window.__VANTIGO_APP__ = { basePath: "/", title: "Vantigo", support: {}, modules };
  const fetchMock = stubFetch((input: RequestInfo | URL) => {
    const url = String(input);
    if (url.startsWith("/api/v1/identity/access/me"))
      return Promise.resolve(json({ permissions, canManageAuthorization: false, administrationScope: "none" }));
    if (url.startsWith("/api/v1/calendar/items"))
      return Promise.resolve(
        json({
          from: "2026-10-19",
          to: "2026-10-25",
          timeZone: "Europe/Oslo",
          items: [],
          publicHolidays: [],
          sources: [],
        }),
      );
    if (url.startsWith("/api/v1/calendar/meta"))
      return Promise.resolve(
        json({ timeZone: "Europe/Oslo", today: "2026-10-19", canViewColleagues: false, sources: [] }),
      );
    return Promise.resolve(new Response(null, { status: 404 }));
  });
  // The page reads its validated search (`Route.useSearch()`), so the test
  // route carries the dashboard's own validator under the same id.
  const root = createRootRoute({ component: Outlet });
  const page = createRoute({
    getParentRoute: () => root,
    path: "/dashboard",
    validateSearch: DashboardRoute.options.validateSearch,
    component: DashboardRoute.options.component as RouteComponent,
  });
  const router = createRouter({
    routeTree: root.addChildren([page]),
    history: createMemoryHistory({ initialEntries: ["/dashboard"] }),
  });
  await router.load();
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <MantineProvider env="test">
      <QueryClientProvider client={queryClient}>
        <RouterProvider router={router as never} />
      </QueryClientProvider>
    </MantineProvider>,
  );
  return fetchMock;
};

afterEach(() => {
  cleanup();
  delete window.__VANTIGO_APP__;
  vi.unstubAllGlobals();
});

// Calendar design D7: the agenda sits at the top of the dashboard when the
// module is on and the caller holds calendar:access, and nowhere otherwise.
describe("the dashboard's agenda", () => {
  it("renders with the calendar module on and calendar:access held", async () => {
    await renderDashboard(["calendar"], ["calendar:access"]);
    expect(await screen.findByText("Today and this week")).toBeInTheDocument();
    // The calendar card is in the module grid too, but nothing in the KPI row
    // or the stats queries: the module serves no stats.
    expect(screen.getByText("Calendar")).toBeInTheDocument();
  });

  it("is absent without calendar:access", async () => {
    const fetchMock = await renderDashboard(["calendar", "time"], ["calendar:view-colleagues", "time:access"]);
    await waitFor(() => expect(screen.getByText("Time")).toBeInTheDocument());
    expect(screen.queryByText("Today and this week")).not.toBeInTheDocument();
    expect(fetchMock.actualCalls.some(([input]) => String(input).startsWith("/api/v1/calendar/"))).toBe(false);
  });

  it("is absent when the calendar module is not enabled", async () => {
    const fetchMock = await renderDashboard(["time"], ["*"]);
    await waitFor(() => expect(screen.getByText("Time")).toBeInTheDocument());
    expect(screen.queryByText("Today and this week")).not.toBeInTheDocument();
    expect(fetchMock.actualCalls.some(([input]) => String(input).startsWith("/api/v1/calendar/"))).toBe(false);
  });
});
