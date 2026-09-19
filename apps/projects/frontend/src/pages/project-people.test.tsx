import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { Project, ProjectRoleAssignment } from "../api/projects";
import { stubFetch } from "../test/fetch";
import { renderWithProviders } from "../test/render";
import { ProjectPeople } from "./project-people";

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const ADA = "11111111-1111-1111-1111-111111111111";
const ALAN = "22222222-2222-2222-2222-222222222222";
const GRACE = "33333333-3333-3333-3333-333333333333";

const project = (canManage: boolean): Project =>
  ({
    id: 7,
    code: "KVEWEBS",
    name: "Website",
    status: "active",
    billingType: "time-and-materials",
    internal: false,
    capabilities: { canManage, canContribute: canManage, canSeeFinancials: canManage, canManageMilestones: canManage },
    billingLinesAvailable: true,
    managers: [],
    revision: 3,
    createdAt: "2026-01-01T10:00:00Z",
    updatedAt: "2026-01-02T10:00:00Z",
  }) as Project;

const roles: ProjectRoleAssignment[] = [
  { userId: ADA, displayName: "Ada Lovelace", role: "manager", active: true, createdAt: "2026-01-01T10:00:00Z" },
  { userId: ALAN, displayName: "Alan Turing", role: "member", active: false, createdAt: "2026-01-01T10:00:00Z" },
];

const stubPeople = (canManage: boolean, assignments: ProjectRoleAssignment[] = roles) =>
  stubFetch((input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input), "http://localhost");
    if (url.pathname === "/api/v1/projects/7") return Promise.resolve(jsonResponse(200, project(canManage)));
    if (url.pathname === "/api/v1/projects/7/roles") return Promise.resolve(jsonResponse(200, assignments));
    if (url.pathname === "/api/v1/projects/7/assignable-users") {
      return Promise.resolve(jsonResponse(200, [{ userId: GRACE, displayName: "Grace Hopper" }]));
    }
    if (url.pathname.startsWith("/api/v1/projects/7/roles/")) {
      if (init?.method === "DELETE") return Promise.resolve(new Response(null, { status: 204 }));
      return Promise.resolve(jsonResponse(200, { ...roles[0], userId: GRACE, displayName: "Grace Hopper" }));
    }
    return Promise.resolve(new Response(null, { status: 404 }));
  });

describe("ProjectPeople", () => {
  it("lists everyone on the project with their role, marking an inactive account", async () => {
    stubPeople(false);
    renderWithProviders(<ProjectPeople projectId={7} />);

    const row = (await screen.findByText("Alan Turing")).closest("tr") as HTMLElement;
    expect(within(row).getByText("Member")).toBeInTheDocument();
    expect(within(row).getByText("Inactive")).toBeInTheDocument();
    expect(screen.getByText("Ada Lovelace")).toBeInTheDocument();
  });

  it("hides adding, changing and removing from a member", async () => {
    stubPeople(false);
    renderWithProviders(<ProjectPeople projectId={7} />);

    await screen.findByText("Ada Lovelace");
    expect(screen.queryByRole("button", { name: "Add person" })).not.toBeInTheDocument();
    expect(screen.queryByRole("combobox", { name: "Change role" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Remove" })).not.toBeInTheDocument();
  });

  it("adds a person in the role the manager chose", async () => {
    const fetchMock = stubPeople(true);
    renderWithProviders(<ProjectPeople projectId={7} />);

    await screen.findByText("Ada Lovelace");
    await userEvent.click(screen.getByRole("button", { name: "Add person" }));

    const dialog = await screen.findByRole("dialog", { name: "Add person" });
    await userEvent.click(within(dialog).getByRole("combobox", { name: "Person" }));
    await userEvent.click(await screen.findByRole("option", { name: "Grace Hopper" }));
    await userEvent.click(within(dialog).getByRole("combobox", { name: "Role" }));
    await userEvent.click(await screen.findByRole("option", { name: "Viewer" }));
    await userEvent.click(within(dialog).getByRole("button", { name: "Add person" }));

    await waitFor(() => {
      const [url, init] = fetchMock.actualCalls.find(([, request]) => request?.method === "PUT") ?? [];
      expect(String(url)).toBe(`/api/v1/projects/7/roles/${GRACE}`);
      expect(JSON.parse(String(init?.body))).toEqual({ role: "viewer" });
    });
  });

  it("changes a role straight from the row", async () => {
    const fetchMock = stubPeople(true);
    renderWithProviders(<ProjectPeople projectId={7} />);

    const row = (await screen.findByText("Ada Lovelace")).closest("tr") as HTMLElement;
    await userEvent.click(within(row).getByRole("combobox", { name: "Change role" }));
    await userEvent.click(await screen.findByRole("option", { name: "Viewer" }));

    await waitFor(() => {
      const [url, init] = fetchMock.actualCalls.find(([, request]) => request?.method === "PUT") ?? [];
      expect(String(url)).toBe(`/api/v1/projects/7/roles/${ADA}`);
      expect(JSON.parse(String(init?.body))).toEqual({ role: "viewer" });
    });
  });

  it("asks before removing someone, and removes them once confirmed", async () => {
    const fetchMock = stubPeople(true);
    renderWithProviders(<ProjectPeople projectId={7} />);

    const row = (await screen.findByText("Alan Turing")).closest("tr") as HTMLElement;
    await userEvent.click(within(row).getByRole("button", { name: "Remove" }));

    const confirm = await screen.findByRole("dialog", { name: "Remove Alan Turing?" });
    await userEvent.click(within(confirm).getByRole("button", { name: "Remove" }));

    await waitFor(() => {
      const [url] = fetchMock.actualCalls.find(([, request]) => request?.method === "DELETE") ?? [];
      expect(String(url)).toBe(`/api/v1/projects/7/roles/${ALAN}`);
    });
  });

  it("cancelling the confirmation removes nobody", async () => {
    const fetchMock = stubPeople(true);
    renderWithProviders(<ProjectPeople projectId={7} />);

    const row = (await screen.findByText("Alan Turing")).closest("tr") as HTMLElement;
    await userEvent.click(within(row).getByRole("button", { name: "Remove" }));
    const confirm = await screen.findByRole("dialog", { name: "Remove Alan Turing?" });
    await userEvent.click(within(confirm).getByRole("button", { name: "Cancel" }));

    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(fetchMock.actualCalls.some(([, init]) => init?.method === "DELETE")).toBe(false);
  });

  it("reports a project it cannot read, rather than a table nobody may act on", async () => {
    stubFetch((input: RequestInfo | URL) => {
      const url = new URL(String(input), "http://localhost");
      if (url.pathname === "/api/v1/projects/7") {
        return Promise.resolve(jsonResponse(500, { title: "Projects are unavailable" }));
      }
      return Promise.resolve(jsonResponse(200, roles));
    });
    renderWithProviders(<ProjectPeople projectId={7} />);

    expect(await screen.findByText("Failed to load the project")).toBeInTheDocument();
    expect(screen.queryByText("Ada Lovelace")).not.toBeInTheDocument();
  });

  it("says when nobody is assigned yet", async () => {
    stubPeople(true, []);
    renderWithProviders(<ProjectPeople projectId={7} />);

    expect(await screen.findByText("Nobody is assigned to this project yet.")).toBeInTheDocument();
  });
});
