import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { Project, TimelineEntry } from "../api/projects";
import { stubFetch } from "../test/fetch";
import { renderWithProviders } from "../test/render";
import { ProjectDetailHeader, ProjectOverview } from "./projects.$projectId";

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const project = (overrides: Partial<Project> = {}): Project => ({
  id: 7,
  code: "KVEWEBS",
  name: "Website",
  description: "The new public site",
  status: "active",
  billingType: "time-and-materials",
  internal: false,
  customerId: 1001,
  customerName: "Kverneland",
  startDate: "2026-01-05",
  endDate: "2026-03-31",
  budgetHours: 120,
  financials: { currency: "NOK", budgetAmount: 50000 },
  capabilities: { canManage: true, canSeeFinancials: true },
  billingLinesAvailable: true,
  managers: [{ userId: "11111111-1111-1111-1111-111111111111", displayName: "Ada Lovelace" }],
  revision: 3,
  createdAt: "2026-01-01T10:00:00Z",
  updatedAt: "2026-01-02T10:00:00Z",
  ...overrides,
});

const timelinePage = (entries: TimelineEntry[]) => ({
  data: entries,
  pagination: {
    page: 1,
    pageSize: 20,
    totalCount: entries.length,
    totalPages: 1,
    hasNextPage: false,
    hasPreviousPage: false,
  },
});

const stubDetail = (row: Project, entries: TimelineEntry[] = []) =>
  stubFetch((input: RequestInfo | URL) => {
    const url = new URL(String(input), "http://localhost");
    if (url.pathname === "/api/v1/projects/7/timeline")
      return Promise.resolve(jsonResponse(200, timelinePage(entries)));
    if (url.pathname === "/api/v1/projects/7/status") return Promise.resolve(jsonResponse(200, row));
    if (url.pathname === "/api/v1/projects/7") return Promise.resolve(jsonResponse(200, row));
    if (url.pathname === "/api/v1/customers") {
      return Promise.resolve(jsonResponse(200, { data: [], pagination: { page: 1, pageSize: 20 } }));
    }
    return Promise.resolve(new Response(null, { status: 404 }));
  });

describe("ProjectDetailHeader", () => {
  it("names the project by code and name, with its status and customer", async () => {
    stubDetail(project());
    renderWithProviders(<ProjectDetailHeader projectId={7} />);

    const heading = await screen.findByRole("heading", { name: /KVEWEBS/ });
    expect(heading).toHaveTextContent("Website");
    expect(screen.getByText("Active")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Kverneland" })).toHaveAttribute("href", "/customers/1001");
  });

  it("calls an internal project internal instead of linking a customer", async () => {
    stubDetail(project({ internal: true, customerId: null, customerName: null }));
    renderWithProviders(<ProjectDetailHeader projectId={7} />);

    expect(await screen.findByText("Internal")).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "Kverneland" })).not.toBeInTheDocument();
  });

  it("hides editing and the status menu from someone who may not manage the project", async () => {
    stubDetail(project({ capabilities: { canManage: false, canSeeFinancials: false }, financials: undefined }));
    renderWithProviders(<ProjectDetailHeader projectId={7} />);

    await screen.findByText("KVEWEBS");
    expect(screen.queryByRole("button", { name: "Edit" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Change status" })).not.toBeInTheDocument();
  });

  it("sets the status the manager picks, and leaves the current one alone", async () => {
    const fetchMock = stubDetail(project());
    renderWithProviders(<ProjectDetailHeader projectId={7} />);

    await screen.findByText("KVEWEBS");
    await userEvent.click(screen.getByRole("button", { name: "Change status" }));
    expect(await screen.findByRole("menuitem", { name: "Active" })).toHaveAttribute("data-disabled");
    await userEvent.click(screen.getByRole("menuitem", { name: "On hold" }));

    await waitFor(() => {
      const [url, init] = fetchMock.actualCalls.find(([, request]) => request?.method === "PUT") ?? [];
      expect(String(url)).toBe("/api/v1/projects/7/status");
      expect(JSON.parse(String(init?.body))).toEqual({ status: "on-hold" });
    });
  });

  it("opens the edit form, the only way into it, with the project loaded", async () => {
    stubDetail(project());
    renderWithProviders(<ProjectDetailHeader projectId={7} />);

    await screen.findByText("KVEWEBS");
    await userEvent.click(screen.getByRole("button", { name: "Edit" }));

    const dialog = await screen.findByRole("dialog", { name: "Edit project" });
    expect(within(dialog).getByLabelText(/project code/i)).toHaveValue("KVEWEBS");
  });

  it("reports a project that cannot be loaded", async () => {
    stubFetch(() => Promise.resolve(jsonResponse(500, { title: "Projects are unavailable" })));
    renderWithProviders(<ProjectDetailHeader projectId={7} />);

    const alert = await screen.findByRole("alert");
    expect(within(alert).getByText("Failed to load the project")).toBeInTheDocument();
  });
});

describe("ProjectOverview", () => {
  it("shows the description, dates, billing type, budget hours and managers", async () => {
    stubDetail(project());
    renderWithProviders(<ProjectOverview projectId={7} />);

    expect(await screen.findByText("The new public site")).toBeInTheDocument();
    const details = screen.getByTestId("project-details");
    expect(details).toHaveTextContent("Time and materials");
    expect(details).toHaveTextContent("120");
    expect(details).toHaveTextContent("Ada Lovelace");
    expect(details).toHaveTextContent("2026");
  });

  it("says so when the project has no description and no manager", async () => {
    stubDetail(project({ description: null, managers: [] }));
    renderWithProviders(<ProjectOverview projectId={7} />);

    expect(await screen.findByText("No description.")).toBeInTheDocument();
    expect(screen.getByText("No manager")).toBeInTheDocument();
  });
});
