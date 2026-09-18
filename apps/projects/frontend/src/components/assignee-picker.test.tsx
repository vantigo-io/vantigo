import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { ProjectPerson, ProjectRoleAssignment } from "../api/people";
import { stubFetch } from "../test/fetch";
import { renderWithProviders } from "../test/render";
import { AssigneePicker } from "./assignee-picker";

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const ADA = "11111111-1111-1111-1111-111111111111";
const ALAN = "22222222-2222-2222-2222-222222222222";
const GRACE = "33333333-3333-3333-3333-333333333333";
const KATHERINE = "44444444-4444-4444-4444-444444444444";

const roles: ProjectRoleAssignment[] = [
  { userId: ADA, displayName: "Ada Lovelace", role: "manager", active: true, createdAt: "2026-01-01T10:00:00Z" },
  { userId: ALAN, displayName: "Alan Turing", role: "member", active: true, createdAt: "2026-01-01T10:00:00Z" },
  { userId: GRACE, displayName: "Grace Hopper", role: "member", active: false, createdAt: "2026-01-01T10:00:00Z" },
];

/** The directory answers whoever it likes; it has already matched on name or email. */
const stubPeople = (assignable: ProjectPerson[] = []) =>
  stubFetch((input: RequestInfo | URL) => {
    const url = new URL(String(input), "http://localhost");
    if (url.pathname === "/api/v1/projects/7/roles") return Promise.resolve(jsonResponse(200, roles));
    if (url.pathname === "/api/v1/projects/7/assignable-users") return Promise.resolve(jsonResponse(200, assignable));
    return Promise.resolve(new Response(null, { status: 404 }));
  });

describe("AssigneePicker", () => {
  it("offers the project's active people, leaving a disabled account out", async () => {
    stubPeople();
    renderWithProviders(<AssigneePicker projectId={7} value={null} onChange={vi.fn()} />);

    await userEvent.click(screen.getByRole("combobox", { name: "Assignee" }));

    expect(await screen.findByRole("option", { name: "Ada Lovelace" })).toBeInTheDocument();
    expect(screen.getByRole("option", { name: "Alan Turing" })).toBeInTheDocument();
    expect(screen.queryByRole("option", { name: "Grace Hopper" })).not.toBeInTheDocument();
  });

  it("narrows the project's own people as the caller types", async () => {
    stubPeople();
    renderWithProviders(<AssigneePicker projectId={7} value={null} onChange={vi.fn()} />);

    const combobox = screen.getByRole("combobox", { name: "Assignee" });
    await userEvent.click(combobox);
    await screen.findByRole("option", { name: "Alan Turing" });
    await userEvent.type(combobox, "ada");

    await waitFor(() => expect(screen.queryByRole("option", { name: "Alan Turing" })).not.toBeInTheDocument());
    expect(screen.getByRole("option", { name: "Ada Lovelace" })).toBeInTheDocument();
  });

  it("keeps whoever the directory answered, whose display name need not contain the term", async () => {
    stubPeople([{ userId: KATHERINE, displayName: "Katherine Johnson" } as ProjectPerson]);
    renderWithProviders(<AssigneePicker projectId={7} value={null} onChange={vi.fn()} />);

    const combobox = screen.getByRole("combobox", { name: "Assignee" });
    await userEvent.click(combobox);
    await userEvent.type(combobox, "kj@example.com");

    expect(await screen.findByRole("option", { name: "Katherine Johnson" })).toBeInTheDocument();
  });

  it("keeps the person already assigned, whatever the search narrows to", async () => {
    stubPeople();
    renderWithProviders(
      <AssigneePicker
        projectId={7}
        value={ADA}
        selected={{ userId: ADA, displayName: "Ada Lovelace", active: true }}
        onChange={vi.fn()}
      />,
    );

    const combobox = screen.getByRole("combobox", { name: "Assignee" });
    expect(combobox).toHaveValue("Ada Lovelace");

    await userEvent.click(combobox);
    await userEvent.clear(combobox);
    await userEvent.type(combobox, "turing");

    await waitFor(() => expect(screen.getByRole("option", { name: "Alan Turing" })).toBeInTheDocument());
    expect(screen.getByRole("option", { name: "Ada Lovelace" })).toBeInTheDocument();
  });
});
