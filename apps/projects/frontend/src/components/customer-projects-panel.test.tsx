import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { ProjectSummary } from "../api/projects";
import { stubFetch } from "../test/fetch";
import { renderWithProviders } from "../test/render";
import { CustomerProjectsPanel } from "./customer-projects-panel";

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const summary = (overrides: Partial<ProjectSummary> = {}): ProjectSummary =>
  ({
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
    managers: [],
    revision: 1,
    createdAt: "2026-01-01T10:00:00Z",
    updatedAt: "2026-01-02T10:00:00Z",
    ...overrides,
  }) as ProjectSummary;

const stubPanel = (rows: ProjectSummary[]) =>
  stubFetch((input: RequestInfo | URL) => {
    const url = new URL(String(input), "http://localhost");
    if (url.pathname === "/api/v1/projects") {
      return Promise.resolve(
        jsonResponse(200, {
          data: rows,
          pagination: {
            page: 1,
            pageSize: 25,
            totalCount: rows.length,
            totalPages: 1,
            hasNextPage: false,
            hasPreviousPage: false,
          },
        }),
      );
    }
    if (url.pathname === "/api/v1/customers") {
      return Promise.resolve(jsonResponse(200, { data: [{ id: 1001, name: "Kverneland" }], pagination: {} }));
    }
    if (url.pathname === "/api/v1/projects/code-suggestion") return Promise.resolve(jsonResponse(200, { code: "KVE" }));
    return Promise.resolve(new Response(null, { status: 404 }));
  });

describe("CustomerProjectsPanel", () => {
  it("lists the customer's projects, each linking to the project", async () => {
    const fetchMock = stubPanel([summary()]);
    renderWithProviders(<CustomerProjectsPanel customerId={1001} canCreate={false} />);

    const link = await screen.findByRole("link", { name: "KVEWEBS" });
    expect(link).toHaveAttribute("href", "/projects/7");
    const row = link.closest("tr") as HTMLElement;
    expect(within(row).getByText("Website")).toBeInTheDocument();
    expect(within(row).getByText("Active")).toBeInTheDocument();
    const [listUrl] = fetchMock.actualCalls.find(([url]) => String(url).includes("/api/v1/projects?")) ?? [];
    expect(String(listUrl)).toContain("customerId=1001");
  });

  it("offers the create button only to someone who may create projects", async () => {
    stubPanel([summary()]);
    renderWithProviders(<CustomerProjectsPanel customerId={1001} canCreate={false} />);

    await screen.findByRole("link", { name: "KVEWEBS" });
    expect(screen.queryByRole("button", { name: "New project" })).not.toBeInTheDocument();
  });

  it("creates a project with the customer already filled in", async () => {
    stubPanel([summary()]);
    renderWithProviders(<CustomerProjectsPanel customerId={1001} canCreate />);

    await screen.findByRole("link", { name: "KVEWEBS" });
    await userEvent.click(screen.getByRole("button", { name: "New project" }));

    const dialog = await screen.findByRole("dialog", { name: "New project" });
    await waitFor(() => expect(within(dialog).getByRole("combobox", { name: /customer/i })).toHaveValue("Kverneland"));
  });

  it("invites the first project when the customer has none", async () => {
    stubPanel([]);
    renderWithProviders(<CustomerProjectsPanel customerId={1001} canCreate />);

    expect(await screen.findByText("No projects yet")).toBeInTheDocument();
    expect(screen.getByText("Create the first project to plan work and bill it.")).toBeInTheDocument();
  });
});
