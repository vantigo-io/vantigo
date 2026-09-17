import { MantineProvider } from "@mantine/core";
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  notFound,
  Outlet,
  RouterProvider,
} from "@tanstack/react-router";
import { render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import "../i18n";
import { appLayoutOptions } from "./-app-layout";

// Drives the real router against a miniature energy app: the layout route
// built from appLayoutOptions, with one child that has a loader. What is
// under test is the gate's placement — a disabled module must stop the child
// loader from running at all, not merely hide its result.
const inject = (modules: string[]) => {
  window.__VANTIGO_APP__ = { basePath: "/", title: "Vantigo", support: {}, modules };
};

afterEach(() => {
  delete window.__VANTIGO_APP__;
});

const renderAt = (path: string, loader: () => unknown) => {
  const rootRoute = createRootRoute({ component: () => <Outlet /> });
  const energyRoute = createRoute({ getParentRoute: () => rootRoute, path: "/energy", ...appLayoutOptions("energy") });
  const meteringPointRoute = createRoute({
    getParentRoute: () => energyRoute,
    path: "/metering-points/$meteringPointId",
    loader,
    component: () => <div>metering point detail</div>,
  });
  const router = createRouter({
    routeTree: rootRoute.addChildren([energyRoute.addChildren([meteringPointRoute])]),
    history: createMemoryHistory({ initialEntries: [path] }),
  });
  render(
    <MantineProvider env="test">
      <RouterProvider router={router as never} />
    </MantineProvider>,
  );
  return router;
};

describe("appLayoutOptions", () => {
  it("renders the app's routes, running their loaders, when the module is enabled", async () => {
    inject(["customers", "energy"]);
    const loader = vi.fn(() => ({ id: 7 }));
    const router = renderAt("/energy/metering-points/7", loader);

    expect(await screen.findByText("metering point detail")).toBeInTheDocument();
    expect(loader).toHaveBeenCalledOnce();
    expect(router.state.matches.map((match) => match.staticData.app)).toEqual([undefined, "energy", undefined]);
  });

  it("renders the not-enabled page in place and never runs child loaders when the module is off", async () => {
    inject(["customers"]);
    const loader = vi.fn(() => ({ id: 7 }));
    const router = renderAt("/energy/metering-points/7", loader);

    expect(await screen.findByRole("heading", { name: "Energy is not enabled" })).toBeInTheDocument();
    expect(screen.getByText(/not enabled in this installation/i)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Go home" })).toBeInTheDocument();
    expect(loader).not.toHaveBeenCalled();
    expect(router.state.location.pathname).toBe("/energy/metering-points/7");
    expect(screen.queryByText("metering point detail")).not.toBeInTheDocument();
  });

  it("treats a missing injection as every module enabled (dev server)", async () => {
    const loader = vi.fn(() => ({ id: 7 }));
    renderAt("/energy/metering-points/7", loader);

    expect(await screen.findByText("metering point detail")).toBeInTheDocument();
    expect(loader).toHaveBeenCalledOnce();
  });

  it("still shows the ordinary not-found page for a genuine 404 inside an enabled app", async () => {
    inject(["energy"]);
    const loader = vi.fn(() => {
      throw notFound();
    });
    renderAt("/energy/metering-points/404", loader);

    expect(await screen.findByRole("heading", { name: "Page not found" })).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Energy is not enabled" })).not.toBeInTheDocument();
  });
});
