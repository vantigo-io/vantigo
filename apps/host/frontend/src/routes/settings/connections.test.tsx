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
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { setLanguagePreference } from "@vantigo/frontend-shell";
import { afterEach, describe, expect, it, vi } from "vitest";
import { renderRoute } from "../../test/render-route";
import { Route as ConnectionsRoute, validateConnectionsSearch } from "./connections";
import "../../i18n";

// The card is the Entra package's own, tested there; here it only shows what the host hands it.
vi.mock("@vantigo/entra-ui", () => ({
  OutlookConnectionCard: (props: { outcome?: { outlook: string; code?: string }; onReturn: (to: string) => void }) => {
    return (
      <section aria-label="Outlook card">
        <p>{props.outcome ? `${props.outcome.outlook} ${props.outcome.code ?? ""}`.trim() : "no outcome"}</p>
        <button type="button" onClick={() => props.onReturn("/calendar/settings")}>
          return
        </button>
      </section>
    );
  },
}));

const renderConnections = (path: string) =>
  renderRoute(path, ConnectionsRoute.options.component as never, new QueryClient());

afterEach(() => {
  cleanup();
  setLanguagePreference("auto");
  delete window.__VANTIGO_APP__;
});

describe("the Connections route's search", () => {
  it("keeps complete, and failed with its code", () => {
    expect(validateConnectionsSearch({ outlook: "complete" })).toEqual({ outlook: "complete" });
    expect(validateConnectionsSearch({ outlook: "failed", code: "access_denied" })).toEqual({
      outlook: "failed",
      code: "access_denied",
    });
  });

  it("drops anything else", () => {
    expect(validateConnectionsSearch({})).toEqual({});
    expect(validateConnectionsSearch({ outlook: "maybe" })).toEqual({});
    expect(validateConnectionsSearch({ outlook: "complete", code: "expired" })).toEqual({ outlook: "complete" });
    expect(validateConnectionsSearch({ outlook: "failed", code: "<script>" })).toEqual({ outlook: "failed" });
    expect(validateConnectionsSearch({ outlook: "failed", code: "x".repeat(41) })).toEqual({ outlook: "failed" });
    expect(validateConnectionsSearch({ outlook: "failed", code: 7 })).toEqual({ outlook: "failed" });
  });

  it("is the route's", () => {
    expect(ConnectionsRoute.options.validateSearch).toBe(validateConnectionsSearch);
  });
});

describe("the Connections route", () => {
  it("names the page and hands the card no outcome when opened plainly", async () => {
    await renderConnections("/settings/connections");
    expect(await screen.findByRole("heading", { name: "Connections" })).toBeInTheDocument();
    expect(screen.getByText("no outcome")).toBeInTheDocument();
  });

  it("names the page in Norwegian", async () => {
    setLanguagePreference("nb");
    await renderConnections("/settings/connections");
    expect(await screen.findByRole("heading", { name: "Tilkoblinger" })).toBeInTheDocument();
  });

  it("hands the card the redirect's outcome, and returns where the card says, replacing the callback's address", async () => {
    const history = createMemoryHistory({
      initialEntries: ["/settings/connections?outlook=failed&code=tenant_mismatch"],
    });
    const root = createRootRoute({ component: Outlet });
    const page = createRoute({
      getParentRoute: () => root,
      path: "/settings/connections",
      validateSearch: validateConnectionsSearch,
      component: ConnectionsRoute.options.component as RouteComponent,
    });
    const router = createRouter({ routeTree: root.addChildren([page]), history });
    await router.load();
    render(
      <MantineProvider env="test">
        <QueryClientProvider client={new QueryClient()}>
          <RouterProvider router={router as never} />
        </QueryClientProvider>
      </MantineProvider>,
    );
    expect(await screen.findByText("failed tenant_mismatch")).toBeInTheDocument();
    const replace = vi.spyOn(history, "replace");
    await userEvent.click(screen.getByRole("button", { name: "return" }));
    expect(replace).toHaveBeenCalledWith("/calendar/settings");
    expect(history.length).toBe(1);
  });

  it("is not found while the Entra module is off", () => {
    window.__VANTIGO_APP__ = { basePath: "/", title: "Vantigo", support: {}, modules: ["calendar"] };
    expect(() => (ConnectionsRoute.options.beforeLoad as () => void)()).toThrow();
    window.__VANTIGO_APP__ = { basePath: "/", title: "Vantigo", support: {}, modules: ["entra"] };
    expect(() => (ConnectionsRoute.options.beforeLoad as () => void)()).not.toThrow();
  });
});
