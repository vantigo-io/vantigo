import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { ProjectTemplate } from "../api/templates";
import { stubFetch } from "../test/fetch";
import { renderWithProviders } from "../test/render";
import { ProjectTemplatesPage } from "./templates";

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const onboarding: ProjectTemplate = {
  id: 4,
  name: "Onboarding",
  description: "Three weeks from kickoff to handover",
  billingType: null,
  milestones: [
    { name: "Kickoff", targetDayOffset: 0 },
    { name: "Handover", targetDayOffset: 21 },
  ],
  tasks: [
    {
      title: "Discovery",
      checklist: ["Book the room"],
      subtasks: [{ title: "Agenda", checklist: ["Draft", "Review"] }, { title: "Invite" }],
    },
    { title: "Design" },
  ],
  dependencies: [{ predecessorTaskIndex: 0, successorTaskIndex: 1 }],
  revision: 1,
  createdAt: "2026-10-31T00:00:00Z",
  updatedAt: "2026-10-31T00:00:00Z",
};

const stubCatalog = (templates: ProjectTemplate[], onDelete?: () => Response) =>
  stubFetch((_url: string | URL | Request, init?: RequestInit) => {
    if (init?.method === "DELETE" && onDelete) return Promise.resolve(onDelete());
    return Promise.resolve(jsonResponse(200, { templates }));
  });

describe("ProjectTemplatesPage", () => {
  it("lists each template with its counts", async () => {
    stubCatalog([onboarding]);
    renderWithProviders(<ProjectTemplatesPage />);

    const row = (await screen.findByText("Onboarding")).closest("tr") as HTMLElement;
    expect(within(row).getByText("Three weeks from kickoff to handover")).toBeInTheDocument();
    // Milestones, tasks, subtasks, checklist items, dependencies.
    expect(
      within(row)
        .getAllByRole("cell")
        .slice(1, 6)
        .map((cell) => cell.textContent),
    ).toEqual(["2", "2", "2", "3", "1"]);
  });

  it("offers New, Edit and Delete", async () => {
    stubCatalog([onboarding]);
    renderWithProviders(<ProjectTemplatesPage />);

    expect(await screen.findByRole("button", { name: "New template" })).toBeInTheDocument();
    await userEvent.click(await screen.findByRole("button", { name: "Actions for Onboarding" }));
    await userEvent.click(await screen.findByRole("menuitem", { name: "Edit" }));
    expect(await screen.findByRole("dialog", { name: "Edit project template" })).toBeInTheDocument();
    expect(screen.getAllByLabelText(/^Title/)[0]).toHaveValue("Discovery");
  });

  it("says how to start when there are none", async () => {
    stubCatalog([]);
    renderWithProviders(<ProjectTemplatesPage />);

    expect(await screen.findByText("No project templates yet")).toBeInTheDocument();
    expect(screen.getByText(/Create one with New template/)).toBeInTheDocument();
  });

  it("deletes a template after confirming", async () => {
    const fetchMock = stubCatalog([onboarding], () => new Response(null, { status: 204 }));
    renderWithProviders(<ProjectTemplatesPage />);

    await userEvent.click(await screen.findByRole("button", { name: "Actions for Onboarding" }));
    await userEvent.click(await screen.findByRole("menuitem", { name: "Delete template" }));
    const confirm = await screen.findByRole("dialog", { name: "Delete Onboarding?" });
    expect(fetchMock.calls.some(([, init]) => init?.method === "DELETE")).toBe(false);
    await userEvent.click(within(confirm).getByRole("button", { name: "Delete template" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/v1/projects/templates/4",
        expect.objectContaining({ method: "DELETE" }),
      ),
    );
    expect(await screen.findByText("Template deleted")).toBeInTheDocument();
  });
});
