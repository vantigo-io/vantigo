import { MantineProvider } from "@mantine/core";
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from "@tanstack/react-router";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { Route as GoogleLayoutRoute } from "./google";

// The layout of Integrations › Google Workspace against a miniature router:
// the real layout component, with a stand-in for each tab's page. What is
// under test is the tab bar following the URL and the URL following the tab bar.
const renderAt = (path: string) => {
  const rootRoute = createRootRoute();
  const layout = createRoute({
    getParentRoute: () => rootRoute,
    path: "/workspace/google",
    component: GoogleLayoutRoute.options.component,
  });
  const page = (name: string) => () => <p>{name} page</p>;
  const tabs = [
    createRoute({ getParentRoute: () => layout, path: "/", component: page("Status") }),
    createRoute({ getParentRoute: () => layout, path: "/connection", component: page("Connection") }),
    createRoute({ getParentRoute: () => layout, path: "/accounts", component: page("Accounts") }),
    createRoute({ getParentRoute: () => layout, path: "/calendar-gmail", component: page("Calendar and Gmail") }),
  ];
  const router = createRouter({
    routeTree: rootRoute.addChildren([layout.addChildren(tabs)]),
    history: createMemoryHistory({ initialEntries: [path] }),
  });
  render(
    <MantineProvider env="test">
      <RouterProvider router={router as never} />
    </MantineProvider>,
  );
  return router;
};

describe("the Google layout's tab bar", () => {
  it("names the page and offers the four tabs, the one for the URL selected", async () => {
    renderAt("/workspace/google/connection");

    expect(await screen.findByRole("heading", { name: "Google Workspace" })).toBeInTheDocument();
    const tabs = screen.getAllByRole("tab");
    expect(tabs.map((tab) => tab.textContent)).toEqual(["Status", "Connection", "Accounts", "Calendar and Gmail"]);
    expect(screen.getByRole("tab", { name: "Connection" })).toHaveAttribute("aria-selected", "true");
    expect(screen.getByRole("tab", { name: "Status" })).toHaveAttribute("aria-selected", "false");
    expect(screen.getByText("Connection page")).toBeInTheDocument();
  });

  it("selects Status on the layout's own address", async () => {
    renderAt("/workspace/google");
    expect(await screen.findByRole("tab", { name: "Status" })).toHaveAttribute("aria-selected", "true");
    expect(screen.getByText("Status page")).toBeInTheDocument();
  });

  it("moves to a tab's route when the tab is chosen", async () => {
    const router = renderAt("/workspace/google");
    await userEvent.click(await screen.findByRole("tab", { name: "Calendar and Gmail" }));

    await waitFor(() => expect(router.state.location.pathname).toBe("/workspace/google/calendar-gmail"));
    expect(await screen.findByText("Calendar and Gmail page")).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "Calendar and Gmail" })).toHaveAttribute("aria-selected", "true");
  });
});
