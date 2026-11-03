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
import {
  type ConnectionButtonProps,
  type ConnectionCardProps,
  ConnectionCards,
  type ConnectionProvider,
  connectionButtonFor,
  enabledConnectionProviders,
  outcomeFor,
  validateConnectionsSearch,
} from "../../lib/connections";
import { renderRoute } from "../../test/render-route";
import { Route as ConnectionsRoute } from "./connections";
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
  OutlookConnectButton: () => <p>Outlook button</p>,
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

  it("keeps the neutral connection=<module> with its outcome, and a failure's code", () => {
    expect(validateConnectionsSearch({ connection: "google", outcome: "complete" })).toEqual({
      connection: "google",
      outcome: "complete",
    });
    expect(validateConnectionsSearch({ connection: "google", outcome: "failed", code: "access_denied" })).toEqual({
      connection: "google",
      outcome: "failed",
      code: "access_denied",
    });
  });

  it("drops a neutral connection without its outcome, or one that is not a short word", () => {
    expect(validateConnectionsSearch({ connection: "google" })).toEqual({});
    expect(validateConnectionsSearch({ outcome: "complete" })).toEqual({});
    expect(validateConnectionsSearch({ connection: "google", outcome: "maybe" })).toEqual({});
    expect(validateConnectionsSearch({ connection: "Google!", outcome: "complete" })).toEqual({});
    expect(validateConnectionsSearch({ connection: "g".repeat(41), outcome: "complete" })).toEqual({});
    expect(validateConnectionsSearch({ connection: "google", outcome: "complete", code: "expired" })).toEqual({
      connection: "google",
      outcome: "complete",
    });
    expect(validateConnectionsSearch({ connection: "google", outcome: "failed", code: "<b>" })).toEqual({
      connection: "google",
      outcome: "failed",
    });
  });

  it("hands each card only its own outcome: Entra's outlook=, any module's connection=", () => {
    expect(outcomeFor("entra", { outlook: "failed", code: "tenant_mismatch" })).toEqual({
      outcome: "failed",
      code: "tenant_mismatch",
    });
    expect(outcomeFor("google", { outlook: "complete" })).toBeUndefined();
    expect(outcomeFor("google", { connection: "google", outcome: "complete" })).toEqual({ outcome: "complete" });
    expect(outcomeFor("entra", { connection: "google", outcome: "complete" })).toBeUndefined();
    expect(outcomeFor("entra", { connection: "entra", outcome: "failed", code: "x" })).toEqual({
      outcome: "failed",
      code: "x",
    });
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

  it("is not found while no connection provider's module is on", () => {
    window.__VANTIGO_APP__ = { basePath: "/", title: "Vantigo", support: {}, modules: ["calendar"] };
    expect(() => (ConnectionsRoute.options.beforeLoad as () => void)()).toThrow();
    window.__VANTIGO_APP__ = { basePath: "/", title: "Vantigo", support: {}, modules: ["entra"] };
    expect(() => (ConnectionsRoute.options.beforeLoad as () => void)()).not.toThrow();
  });
});

// Two stand-in providers: the list's composition is the host's; each provider's own rendering is its package's.
const fakeCard =
  (name: string) =>
  ({ outcome }: ConnectionCardProps) => (
    <p>
      {name} card{outcome ? ` ${outcome.outcome}${outcome.code ? ` ${outcome.code}` : ""}` : ""}
    </p>
  );
const fakeButton =
  (name: string) =>
  ({ feature, returnTo }: ConnectionButtonProps) => (
    <p>
      {name} button for {feature}, back to {returnTo}
    </p>
  );
const twoProviders: ConnectionProvider[] = [
  { module: "entra", Card: fakeCard("Entra"), Button: fakeButton("Entra") },
  { module: "calendar", Card: fakeCard("Other"), Button: fakeButton("Other") },
];

describe("the host's list of connection providers", () => {
  it("keeps the providers whose module is on, in the list's order", () => {
    expect(enabledConnectionProviders(twoProviders, ["calendar", "entra"]).map((p) => p.module)).toEqual([
      "entra",
      "calendar",
    ]);
    expect(enabledConnectionProviders(twoProviders, ["calendar"]).map((p) => p.module)).toEqual(["calendar"]);
    expect(enabledConnectionProviders(twoProviders, [])).toEqual([]);
  });

  it("renders every enabled provider's card, each with only its own outcome, and a disabled one's not", () => {
    render(
      <ConnectionCards
        providers={enabledConnectionProviders(twoProviders, ["entra", "calendar"])}
        search={{ connection: "calendar", outcome: "failed", code: "access_denied" }}
        onReturn={() => {}}
      />,
    );
    expect(screen.getByText("Entra card")).toBeInTheDocument();
    expect(screen.getByText("Other card failed access_denied")).toBeInTheDocument();
    cleanup();
    render(
      <ConnectionCards
        providers={enabledConnectionProviders(twoProviders, ["calendar"])}
        search={{}}
        onReturn={() => {}}
      />,
    );
    expect(screen.queryByText(/Entra card/)).not.toBeInTheDocument();
    expect(screen.getByText("Other card")).toBeInTheDocument();
  });

  it("composes every enabled provider's button into the connection.button slot, and none when none is on", () => {
    const both = connectionButtonFor(enabledConnectionProviders(twoProviders, ["entra", "calendar"]));
    render(<div>{both?.({ feature: "mail", returnTo: "/communications/inbox" })}</div>);
    expect(screen.getByText("Entra button for mail, back to /communications/inbox")).toBeInTheDocument();
    expect(screen.getByText("Other button for mail, back to /communications/inbox")).toBeInTheDocument();
    cleanup();
    const one = connectionButtonFor(enabledConnectionProviders(twoProviders, ["calendar"]));
    render(<div>{one?.({ feature: "calendar", returnTo: "/calendar/settings" })}</div>);
    expect(screen.queryByText(/Entra button/)).not.toBeInTheDocument();
    expect(screen.getByText("Other button for calendar, back to /calendar/settings")).toBeInTheDocument();
    expect(connectionButtonFor(enabledConnectionProviders(twoProviders, ["customers"]))).toBeUndefined();
  });
});
