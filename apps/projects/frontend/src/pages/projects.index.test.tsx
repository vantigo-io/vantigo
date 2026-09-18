import { MantineProvider } from "@mantine/core";
import { ModalsProvider } from "@mantine/modals";
import { Notifications } from "@mantine/notifications";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, createRouter, RouterProvider } from "@tanstack/react-router";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { ProjectSummary } from "../api/projects";
import { stubFetch } from "../test/fetch";
import { makeRouteTree } from "../test/route-tree";

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const summary = (overrides: Partial<ProjectSummary>): ProjectSummary => ({
  id: 7,
  code: "KVEWEBS",
  name: "Website",
  status: "active",
  billingType: "time-and-materials",
  internal: false,
  customerId: 1001,
  customerName: "Kverneland",
  startDate: "2026-01-05",
  endDate: "2026-03-31",
  budgetHours: 120,
  managers: [
    { userId: "11111111-1111-1111-1111-111111111111", displayName: "Ada Lovelace" },
    { userId: "22222222-2222-2222-2222-222222222222", displayName: "Alan Turing" },
  ],
  revision: 1,
  createdAt: "2026-01-01T10:00:00Z",
  updatedAt: "2026-01-02T10:00:00Z",
  ...overrides,
});

const page = (rows: ProjectSummary[]) => ({
  data: rows,
  pagination: {
    page: 1,
    pageSize: 25,
    totalCount: rows.length,
    totalPages: 1,
    hasNextPage: false,
    hasPreviousPage: false,
  },
});

const stubProjects = (list: Response) =>
  stubFetch((input: RequestInfo | URL) => {
    const url = new URL(String(input), "http://localhost");
    if (url.pathname === "/api/v1/projects/stats") {
      return Promise.resolve(jsonResponse(200, { planned: 4, active: 9, onHold: 2, completed: 11, cancelled: 1 }));
    }
    if (url.pathname === "/api/v1/projects") return Promise.resolve(list.clone());
    if (url.pathname === "/api/v1/customers") {
      return Promise.resolve(jsonResponse(200, { data: [], pagination: { page: 1, pageSize: 20 } }));
    }
    return Promise.resolve(new Response(null, { status: 404 }));
  });

const renderPage = (url = "/projects", canCreate = true) => {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const router = createRouter({
    routeTree: makeRouteTree(canCreate),
    context: { queryClient },
    history: createMemoryHistory({ initialEntries: [url] }),
  });
  render(
    <MantineProvider env="test">
      <Notifications />
      <ModalsProvider>
        <QueryClientProvider client={queryClient}>
          <RouterProvider router={router} />
        </QueryClientProvider>
      </ModalsProvider>
    </MantineProvider>,
  );
  return { router };
};

describe("ProjectsPage", () => {
  it("lists a project with its customer, status, first manager and dates", async () => {
    stubProjects(jsonResponse(200, page([summary({})])));
    renderPage();

    const row = (await screen.findByRole("link", { name: "KVEWEBS" })).closest("tr") as HTMLElement;
    expect(within(row).getByRole("link", { name: "KVEWEBS" })).toHaveAttribute("href", "/projects/7");
    expect(within(row).getByText("Website")).toBeInTheDocument();
    expect(within(row).getByText("Kverneland")).toBeInTheDocument();
    expect(within(row).getByText("Active")).toBeInTheDocument();
    expect(within(row).getByText("Ada Lovelace")).toBeInTheDocument();
    expect(within(row).getByText("+1")).toBeInTheDocument();
    expect(row).toHaveTextContent("2026");
  });

  it("counts the active, planned and on-hold projects", async () => {
    stubProjects(jsonResponse(200, page([summary({})])));
    renderPage();

    const kpis = await screen.findByTestId("projects-kpis");
    expect(within(kpis).getByText("Active").parentElement).toHaveTextContent("9");
    expect(within(kpis).getByText("Planned").parentElement).toHaveTextContent("4");
    expect(within(kpis).getByText("On hold").parentElement).toHaveTextContent("2");
  });

  it("marks a project without a customer as internal", async () => {
    stubProjects(
      jsonResponse(
        200,
        page([
          summary({ id: 8, code: "INTOFFS", name: "Offsite", internal: true, customerId: null, customerName: null }),
        ]),
      ),
    );
    renderPage();

    const row = (await screen.findByRole("link", { name: "INTOFFS" })).closest("tr") as HTMLElement;
    expect(within(row).getByText("Internal")).toBeInTheDocument();
  });

  it("shows an empty state when nothing matches", async () => {
    stubProjects(jsonResponse(200, page([])));
    renderPage();

    expect(await screen.findByText("No projects found.")).toBeInTheDocument();
  });

  it("shows an alert when the list cannot be loaded", async () => {
    stubProjects(jsonResponse(500, { title: "Projects are unavailable" }));
    renderPage();

    const alert = await screen.findByRole("alert");
    expect(within(alert).getByText("Failed to load projects")).toBeInTheDocument();
  });

  it("offers the create button only to someone who may create projects", async () => {
    stubProjects(jsonResponse(200, page([summary({})])));
    renderPage("/projects", false);

    await screen.findByRole("link", { name: "KVEWEBS" });
    expect(screen.queryByRole("button", { name: "New project" })).not.toBeInTheDocument();
  });

  it("narrows the list to the caller's own projects, back on the first page", async () => {
    stubProjects(jsonResponse(200, page([summary({})])));
    const { router } = renderPage("/projects?page=3");

    await screen.findByRole("link", { name: "KVEWEBS" });
    await userEvent.click(screen.getByLabelText("My projects"));

    await waitFor(() => expect(router.state.location.search).toMatchObject({ mine: true, page: 1 }));
  });

  it("filters by status without losing the other filters", async () => {
    stubProjects(jsonResponse(200, page([summary({})])));
    const { router } = renderPage("/projects?page=2&mine=true");

    await screen.findByRole("link", { name: "KVEWEBS" });
    await userEvent.click(screen.getByRole("combobox", { name: "Status" }));
    await userEvent.click(await screen.findByRole("option", { name: "On hold" }));

    await waitFor(() => expect(router.state.location.search).toMatchObject({ status: "on-hold", mine: true, page: 1 }));
  });

  it("opens the create form when the URL asks for it, and drops the intent when the form closes", async () => {
    stubProjects(jsonResponse(200, page([summary({})])));
    const { router } = renderPage("/projects?create=true");

    const dialog = await screen.findByRole("dialog", { name: "New project" });
    await userEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));

    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(router.state.location.search).not.toHaveProperty("create");
  });

  it("never opens the create form for someone who may not create projects", async () => {
    stubProjects(jsonResponse(200, page([summary({})])));
    const { router } = renderPage("/projects?create=true", false);

    await screen.findByRole("link", { name: "KVEWEBS" });
    await waitFor(() => expect(router.state.location.search).not.toHaveProperty("create"));
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });
});
