import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { DeliveryMilestone } from "../api/delivery-milestones";
import { stubFetch } from "../test/fetch";
import { renderWithProviders } from "../test/render";
import { DeliveryMilestonesCard } from "./-delivery-milestones-card";

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const sampleMilestone: DeliveryMilestone = {
  id: 1,
  projectId: 7,
  name: "Phase 1 MVP",
  description: "Initial deliverable",
  targetDate: "2026-11-15",
  status: "open",
  position: 1,
  totalTasks: 4,
  completedTasks: 2,
  progressPercent: 50,
  estimatedHoursTotal: 20,
  estimatedHoursCompleted: 10,
  revision: 1,
  createdAt: "2026-10-31T00:00:00Z",
  updatedAt: "2026-10-31T00:00:00Z",
};

const stubMilestones = (milestones: DeliveryMilestone[]) =>
  stubFetch((input: RequestInfo | URL) => {
    const url = new URL(String(input), "http://localhost");
    if (url.pathname === "/api/v1/projects/7/delivery-milestones") {
      return Promise.resolve(jsonResponse(200, { milestones }));
    }
    return Promise.resolve(new Response(null, { status: 404 }));
  });

describe("DeliveryMilestonesCard", () => {
  it("renders delivery milestones with derived progress", async () => {
    stubMilestones([sampleMilestone]);
    const onSelectMilestone = vi.fn();

    renderWithProviders(
      <DeliveryMilestonesCard
        projectId={7}
        canContribute={true}
        selectedMilestoneId={null}
        onSelectMilestone={onSelectMilestone}
      />,
    );

    expect(await screen.findByText("Phase 1 MVP")).toBeInTheDocument();
    expect(screen.getByText("2/4 tasks (50 %)")).toBeInTheDocument();
    expect(screen.getByText("10 / 20 h")).toBeInTheDocument();
  });

  it("selects milestone on click", async () => {
    stubMilestones([sampleMilestone]);
    const onSelectMilestone = vi.fn();

    renderWithProviders(
      <DeliveryMilestonesCard
        projectId={7}
        canContribute={true}
        selectedMilestoneId={null}
        onSelectMilestone={onSelectMilestone}
      />,
    );

    const card = await screen.findByText("Phase 1 MVP");
    await userEvent.click(card);

    expect(onSelectMilestone).toHaveBeenCalledWith(1);
  });

  it("renders empty state when there are no milestones", async () => {
    stubMilestones([]);
    const onSelectMilestone = vi.fn();

    renderWithProviders(
      <DeliveryMilestonesCard
        projectId={7}
        canContribute={true}
        selectedMilestoneId={null}
        onSelectMilestone={onSelectMilestone}
      />,
    );

    expect(await screen.findByText("No delivery milestones yet.")).toBeInTheDocument();
  });
});
