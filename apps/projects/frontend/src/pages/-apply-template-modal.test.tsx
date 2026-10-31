import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { ProjectTemplate } from "../api/templates";
import { stubFetch } from "../test/fetch";
import { renderWithProviders } from "../test/render";
import { ApplyTemplateModal } from "./-apply-template-modal";

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const sampleTemplate: ProjectTemplate = {
  id: 1,
  name: "Client Onboarding Blueprint",
  description: "Standard 3-week onboarding process",
  billingType: "fixed_price",
  milestones: [
    {
      name: "Kickoff & Discovery",
      description: "Week 1 alignment",
      targetDayOffset: 7,
    },
    {
      name: "Final Sign-off",
      description: "Handover complete",
      targetDayOffset: 21,
    },
  ],
  tasks: [
    {
      title: "Discovery Call",
      description: "Meet key stakeholders",
      startDayOffset: 1,
      dueDayOffset: 3,
      milestoneIndex: 0,
      subtasks: [
        {
          title: "Prepare agenda",
          startDayOffset: 1,
          dueDayOffset: 1,
        },
      ],
    },
    {
      title: "Solution Architecture",
      description: "Draft system design",
      startDayOffset: 4,
      dueDayOffset: 10,
      milestoneIndex: 1,
    },
  ],
  dependencies: [
    {
      predecessorTaskIndex: 0,
      successorTaskIndex: 1,
    },
  ],
  revision: 1,
  createdAt: "2026-10-31T00:00:00Z",
  updatedAt: "2026-10-31T00:00:00Z",
};

describe("ApplyTemplateModal", () => {
  it("renders template options, selects blueprint preview, and applies template", async () => {
    const fetchMock = stubFetch((url: string | URL | Request) => {
      if (typeof url === "string" && url.endsWith("/templates")) {
        return Promise.resolve(jsonResponse(200, { templates: [sampleTemplate] }));
      }
      if (typeof url === "string" && url.endsWith("/apply-template")) {
        return Promise.resolve(
          jsonResponse(200, {
            milestonesCreated: 2,
            tasksCreated: 3,
            dependenciesCreated: 1,
            createdMilestoneIds: [10, 11],
            createdTaskIds: [100, 101, 102],
          }),
        );
      }
      return Promise.resolve(new Response(null, { status: 404 }));
    });

    const onClose = vi.fn();
    renderWithProviders(
      <ApplyTemplateModal projectId={7} projectStartDate="2026-11-01" opened={true} onClose={onClose} />,
    );

    // Initial state: select template
    const templateSelect = await screen.findByRole("combobox", { name: /^Template/ });
    expect(templateSelect).toBeInTheDocument();

    // Select the template
    await userEvent.click(templateSelect);
    const option = await screen.findByRole("option", { name: "Client Onboarding Blueprint" });
    await userEvent.click(option);

    // Preview should display milestones and tasks
    expect(await screen.findByText("Kickoff & Discovery")).toBeInTheDocument();
    expect(screen.getByText("Final Sign-off")).toBeInTheDocument();
    expect(screen.getByText("Milestones (2)")).toBeInTheDocument();
    expect(screen.getByText("Tasks (2)")).toBeInTheDocument();

    // Click apply button
    const applyButton = screen.getByRole("button", { name: "Apply template" });
    await userEvent.click(applyButton);

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith("/api/v1/projects/7/apply-template", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          templateId: 1,
          kickoffDate: "2026-11-01",
        }),
      }),
    );

    expect(onClose).toHaveBeenCalled();
  });

  it("closes modal on cancel", async () => {
    stubFetch(() => Promise.resolve(jsonResponse(200, { templates: [sampleTemplate] })));
    const onClose = vi.fn();
    renderWithProviders(
      <ApplyTemplateModal projectId={7} projectStartDate="2026-11-01" opened={true} onClose={onClose} />,
    );

    const cancelButton = await screen.findByRole("button", { name: "Cancel" });
    await userEvent.click(cancelButton);

    expect(onClose).toHaveBeenCalled();
  });
});
