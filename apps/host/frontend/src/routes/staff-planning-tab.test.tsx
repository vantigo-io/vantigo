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
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { stubFetch } from "../test/fetch";
import { Route as StaffPersonRoute } from "./staff/people.$id";
import "../i18n";

// Staff's page and Planning's timeline are their packages' own, tested there;
// here they only show which extra tabs the host route hands the page.
vi.mock("@vantigo/staff-ui/pages/person", () => ({
  PersonPage: ({ extraTabs }: { extraTabs?: readonly { value: string; label: ReactNode }[] }) => (
    <ul aria-label="Extra tabs">
      {(extraTabs ?? []).map((tab) => (
        <li key={tab.value}>{tab.label}</li>
      ))}
    </ul>
  ),
}));
vi.mock("@vantigo/planning-ui", () => ({ PersonTimeline: () => null }));

const json = (body: unknown) =>
  new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } });

/**
 * Staff's person page for person 1 with the modules named enabled, the
 * permissions named held and the person answer's `capabilities.self`.
 */
const renderPersonPage = async (modules: string[], permissions: string[], self: boolean) => {
  window.__VANTIGO_APP__ = { basePath: "/", title: "Vantigo", support: {}, modules };
  const fetchMock = stubFetch((input: RequestInfo | URL) => {
    const url = String(input);
    if (url.startsWith("/api/v1/identity/access/me"))
      return Promise.resolve(json({ permissions, canManageAuthorization: false, administrationScope: "none" }));
    if (url.startsWith("/api/v1/staff/people/1")) return Promise.resolve(json({ id: 1, capabilities: { self } }));
    return Promise.resolve(new Response(null, { status: 404 }));
  });
  // The page reads its params through `Route.useParams()`, so the test route
  // has the same path, and with it the same id.
  const root = createRootRoute({ component: Outlet });
  const page = createRoute({
    getParentRoute: () => root,
    path: "/staff/people/$id",
    component: StaffPersonRoute.options.component as RouteComponent,
  });
  const router = createRouter({
    routeTree: root.addChildren([page]),
    history: createMemoryHistory({ initialEntries: ["/staff/people/1"] }),
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
  return { fetchMock, queryClient };
};

/** Waits until the route's reads have settled, so an absent tab is absent for good. */
const settled = async (queryClient: QueryClient) => {
  await screen.findByRole("list", { name: "Extra tabs" });
  await waitFor(() => expect(queryClient.isFetching()).toBe(0));
};

afterEach(() => {
  cleanup();
  delete window.__VANTIGO_APP__;
  vi.unstubAllGlobals();
});

describe("the Planning tab on Staff's person page", () => {
  it("is drawn for planning:view, on anyone's page", async () => {
    await renderPersonPage(["staff", "planning"], ["planning:access", "planning:view"], false);
    expect(await screen.findByRole("listitem")).toHaveTextContent("Planning");
  });

  it("is drawn for the person themselves with planning:access alone", async () => {
    await renderPersonPage(["staff", "planning"], ["planning:access"], true);
    expect(await screen.findByRole("listitem")).toHaveTextContent("Planning");
  });

  it("is not drawn for planning:access alone on someone else's page", async () => {
    const { fetchMock, queryClient } = await renderPersonPage(["staff", "planning"], ["planning:access"], false);
    await waitFor(() => {
      const asked = fetchMock.actualCalls.map(([url]) => String(url));
      expect(asked.some((url) => url.startsWith("/api/v1/identity/access/me"))).toBe(true);
      expect(asked.some((url) => url.startsWith("/api/v1/staff/people/1"))).toBe(true);
    });
    await settled(queryClient);
    expect(screen.queryByRole("listitem")).not.toBeInTheDocument();
  });

  it("is not drawn when the planning module is off, and nothing is asked for it", async () => {
    const { fetchMock, queryClient } = await renderPersonPage(["staff"], ["*"], true);
    await settled(queryClient);
    expect(screen.queryByRole("listitem")).not.toBeInTheDocument();
    const asked = fetchMock.actualCalls.map(([url]) => String(url));
    expect(asked.some((url) => url.startsWith("/api/v1/identity/access/me"))).toBe(false);
    expect(asked.some((url) => url.startsWith("/api/v1/staff/people/1"))).toBe(false);
  });
});
