import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { DeliveryMilestone } from "../api/delivery-milestones";
import { stubFetch } from "../test/fetch";
import { renderWithProviders } from "../test/render";
import { DeliveryMilestoneFormModal, type DeliveryMilestoneModalState } from "./-delivery-milestone-form-modal";

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
  totalTasks: 0,
  completedTasks: 0,
  progressPercent: 0,
  revision: 1,
  createdAt: "2026-10-31T00:00:00Z",
  updatedAt: "2026-10-31T00:00:00Z",
};

const stubSave = (response: () => Response) => stubFetch(() => Promise.resolve(response()));

const renderModal = (state: DeliveryMilestoneModalState | null) => {
  const onClose = vi.fn();
  renderWithProviders(<DeliveryMilestoneFormModal projectId={7} state={state} onClose={onClose} />);
  return { onClose };
};

describe("DeliveryMilestoneFormModal", () => {
  it("creates a delivery milestone", async () => {
    const fetchMock = stubSave(() => jsonResponse(201, sampleMilestone));
    const { onClose } = renderModal({ mode: "create" });

    await userEvent.type(await screen.findByLabelText(/^Name/), "Phase 1 MVP");
    await userEvent.type(screen.getByLabelText(/^Description/), "Initial deliverable");
    await userEvent.click(screen.getByRole("button", { name: "Create" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith("/api/v1/projects/7/delivery-milestones", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ name: "Phase 1 MVP", description: "Initial deliverable" }),
      }),
    );
    expect(onClose).toHaveBeenCalled();
  });

  it("edits an existing delivery milestone", async () => {
    const fetchMock = stubSave(() => jsonResponse(200, { ...sampleMilestone, name: "Phase 1 Updated", revision: 2 }));
    const { onClose } = renderModal({ mode: "edit", milestone: sampleMilestone });

    const nameInput = await screen.findByLabelText(/^Name/);
    await userEvent.clear(nameInput);
    await userEvent.type(nameInput, "Phase 1 Updated");
    await userEvent.click(screen.getByRole("button", { name: "Save changes" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith("/api/v1/projects/delivery-milestones/1", {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          name: "Phase 1 Updated",
          description: "Initial deliverable",
          targetDate: "2026-11-15",
          revision: 1,
        }),
      }),
    );
    expect(onClose).toHaveBeenCalled();
  });

  it("requires a non-empty name", async () => {
    renderModal({ mode: "create" });

    await userEvent.click(await screen.findByRole("button", { name: "Create" }));

    expect(await screen.findByText("A milestone name is required")).toBeInTheDocument();
  });
});
