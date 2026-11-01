import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { ProjectTemplate } from "../api/templates";
import { type StubbedFetch, stubFetch } from "../test/fetch";
import { renderWithProviders } from "../test/render";
import { TemplateFormModal, type TemplateModalState } from "./-template-form-modal";

const jsonResponse = (status: number, body: unknown, contentType = "application/json") =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": contentType } });

const stored: ProjectTemplate = {
  id: 4,
  name: "Onboarding",
  description: "Three weeks",
  billingType: "fixed-price",
  milestones: [{ name: "Kickoff", description: null, targetDayOffset: 0 }],
  tasks: [
    { title: "Discovery", startDayOffset: 0, dueDayOffset: 3, milestoneIndex: 0 },
    { title: "Design", startDayOffset: 4, dueDayOffset: 10 },
    { title: "Build" },
  ],
  dependencies: [
    { predecessorTaskIndex: 0, successorTaskIndex: 1 },
    { predecessorTaskIndex: 1, successorTaskIndex: 2 },
  ],
  revision: 5,
  createdAt: "2026-10-31T00:00:00Z",
  updatedAt: "2026-10-31T00:00:00Z",
};

const renderModal = (state: TemplateModalState | null) => {
  const onClose = vi.fn();
  renderWithProviders(<TemplateFormModal state={state} onClose={onClose} />);
  return { onClose };
};

const sentBody = (fetchMock: StubbedFetch) => {
  const init = fetchMock.calls.at(-1)?.[1];
  return JSON.parse(String(init?.body));
};

const pickOption = async (input: HTMLElement, option: string) => {
  await userEvent.click(input);
  await userEvent.click(await screen.findByRole("option", { name: option }));
};

describe("TemplateFormModal", () => {
  it("creates a template with a milestone, linked tasks, a subtask and a dependency", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(201, { ...stored, name: "Launch" })));
    const { onClose } = renderModal({ mode: "create" });

    const [templateName] = await screen.findAllByLabelText(/^Name/);
    await userEvent.type(templateName as HTMLElement, "Launch");

    await userEvent.click(screen.getByRole("button", { name: "Add milestone" }));
    await userEvent.type(screen.getAllByLabelText(/^Name/)[1] as HTMLElement, "Go-live");
    const target = screen.getByLabelText(/^Target day/);
    await userEvent.clear(target);
    await userEvent.type(target, "14");

    await userEvent.click(screen.getByRole("button", { name: "Add task" }));
    await userEvent.click(screen.getByRole("button", { name: "Add task" }));
    const titles = screen.getAllByLabelText(/^Title/);
    await userEvent.type(titles[0] as HTMLElement, "Prepare");
    await userEvent.type(titles[1] as HTMLElement, "Release");
    await pickOption(screen.getAllByRole("combobox", { name: "Delivery milestone" })[1] as HTMLElement, "Go-live");
    await userEvent.type(screen.getAllByLabelText(/^Due day/)[1] as HTMLElement, "14");

    await userEvent.click(screen.getAllByRole("button", { name: "Add subtask" })[0] as HTMLElement);
    await userEvent.type(screen.getByLabelText(/^Subtask title/), "Checklist");

    await userEvent.click(screen.getByRole("button", { name: "Add dependency" }));
    await pickOption(screen.getByRole("combobox", { name: /^Predecessor/ }), "Prepare");
    await pickOption(screen.getByRole("combobox", { name: /^Successor/ }), "Release");

    await userEvent.click(screen.getByRole("button", { name: "Create" }));

    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(fetchMock.calls.at(-1)?.[0]).toBe("/api/v1/projects/templates");
    expect(fetchMock.calls.at(-1)?.[1]?.method).toBe("POST");
    expect(sentBody(fetchMock)).toEqual({
      name: "Launch",
      milestones: [{ name: "Go-live", targetDayOffset: 14 }],
      tasks: [
        { title: "Prepare", subtasks: [{ title: "Checklist" }] },
        { title: "Release", dueDayOffset: 14, milestoneIndex: 0, subtasks: [] },
      ],
      dependencies: [{ predecessorTaskIndex: 0, successorTaskIndex: 1 }],
    });
  });

  it("re-numbers the dependencies when a task is removed, and sends the revision and billing type", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(200, { ...stored, revision: 6 })));
    const { onClose } = renderModal({ mode: "edit", template: stored });

    await userEvent.click(await screen.findByRole("button", { name: "Remove Discovery" }));
    await userEvent.click(screen.getByRole("button", { name: "Save changes" }));

    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(fetchMock.calls.at(-1)?.[0]).toBe("/api/v1/projects/templates/4");
    expect(fetchMock.calls.at(-1)?.[1]?.method).toBe("PUT");
    const body = sentBody(fetchMock);
    expect(body.revision).toBe(5);
    expect(body.billingType).toBe("fixed-price");
    expect(body.tasks.map((task: { title: string }) => task.title)).toEqual(["Design", "Build"]);
    // Discovery → Design went with Discovery; Design → Build is now 0 → 1.
    expect(body.dependencies).toEqual([{ predecessorTaskIndex: 0, successorTaskIndex: 1 }]);
  });

  it("re-points a task's milestone link when the milestone moves", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(200, stored)));
    renderModal({
      mode: "edit",
      template: {
        ...stored,
        milestones: [...stored.milestones, { name: "Handover", targetDayOffset: 21 }],
        dependencies: [],
      },
    });

    await userEvent.click(await screen.findByRole("button", { name: "Move Handover up" }));
    await userEvent.click(screen.getByRole("button", { name: "Save changes" }));

    await waitFor(() => expect(fetchMock.calls).toHaveLength(1));
    const body = sentBody(fetchMock);
    expect(body.milestones.map((m: { name: string }) => m.name)).toEqual(["Handover", "Kickoff"]);
    expect(body.tasks[0].milestoneIndex).toBe(1);
  });

  it("refuses a cycle and a missing title before sending anything", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(200, stored)));
    renderModal({
      mode: "edit",
      template: {
        ...stored,
        dependencies: [...stored.dependencies, { predecessorTaskIndex: 2, successorTaskIndex: 0 }],
      },
    });

    await userEvent.clear(screen.getAllByLabelText(/^Title/)[1] as HTMLElement);
    await userEvent.click(screen.getByRole("button", { name: "Save changes" }));

    expect(await screen.findByText(/These dependencies form a loop/)).toBeInTheDocument();
    expect(screen.getByText("A task title is required")).toBeInTheDocument();
    expect(fetchMock.calls).toHaveLength(0);
  });

  it("shows the server's field errors next to the field", async () => {
    stubFetch(() =>
      Promise.resolve(
        jsonResponse(
          400,
          { title: "Invalid template", status: 400, errors: { "milestones[0].name": ["Taken by the server"] } },
          "application/problem+json",
        ),
      ),
    );
    renderModal({ mode: "edit", template: stored });

    await userEvent.click(await screen.findByRole("button", { name: "Save changes" }));

    expect(await screen.findByText("Taken by the server")).toBeInTheDocument();
    expect(screen.getAllByLabelText(/^Name/)[1]).toHaveAttribute("aria-invalid", "true");
  });

  it("says the template changed elsewhere on a stale revision", async () => {
    stubFetch(() =>
      Promise.resolve(
        jsonResponse(409, { title: "Revision conflict", status: 409, detail: "stale" }, "application/problem+json"),
      ),
    );
    const { onClose } = renderModal({ mode: "edit", template: stored });

    await userEvent.click(await screen.findByRole("button", { name: "Save changes" }));

    expect(await screen.findByText(/The template was changed by someone else/)).toBeInTheDocument();
    expect(onClose).not.toHaveBeenCalled();
  });

  it("offers no dependency until there are two tasks to link", async () => {
    renderModal({ mode: "create" });
    const add = await screen.findByRole("button", { name: "Add dependency" });
    expect(add).toBeDisabled();
    await userEvent.click(screen.getByRole("button", { name: "Add task" }));
    expect(add).toBeDisabled();
    await userEvent.click(screen.getByRole("button", { name: "Add task" }));
    expect(add).toBeEnabled();
    expect(within(screen.getByRole("dialog")).getAllByLabelText(/^Title/)).toHaveLength(2);
  });
});
