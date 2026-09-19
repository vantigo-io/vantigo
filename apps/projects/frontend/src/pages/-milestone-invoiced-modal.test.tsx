import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { BillingMilestone } from "../api/milestones";
import { stubFetch } from "../test/fetch";
import { renderWithProviders } from "../test/render";
import { MilestoneInvoicedModal } from "./-milestone-invoiced-modal";

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const milestone: BillingMilestone = {
  id: 12,
  projectId: 7,
  name: "Kick-off",
  amount: 300000,
  effectiveAmount: 300000,
  currency: "NOK",
  status: "ready",
  position: 1,
  overdue: false,
  revision: 4,
  createdAt: "2026-01-01T10:00:00Z",
  updatedAt: "2026-01-01T10:00:00Z",
  capabilities: {
    canEdit: false,
    canDelete: false,
    canMarkReady: false,
    canMarkPlanned: true,
    canMarkInvoiced: true,
    canUndoInvoiced: false,
    canCancel: true,
    canReopen: false,
  },
} as BillingMilestone;

const renderModal = (row: BillingMilestone | null = milestone) => {
  const onClose = vi.fn();
  renderWithProviders(<MilestoneInvoicedModal milestone={row} onClose={onClose} />);
  return { onClose };
};

describe("MilestoneInvoicedModal", () => {
  it("names the milestone it is about to invoice", async () => {
    stubFetch(() => Promise.resolve(jsonResponse(200, milestone)));
    renderModal();

    expect(await screen.findByRole("dialog", { name: "Mark Kick-off as invoiced" })).toBeInTheDocument();
  });

  it("sends the reference the caller typed along with the revision", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(200, milestone)));
    const { onClose } = renderModal();

    await userEvent.type(await screen.findByLabelText("Invoice reference"), "2026-0042");
    await userEvent.click(screen.getByRole("button", { name: "Mark as invoiced" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith("/api/v1/projects/milestones/12/status", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ status: "invoiced", revision: 4, invoiceReference: "2026-0042" }),
      }),
    );
    expect(onClose).toHaveBeenCalled();
  });

  it("sends neither invoice field when both are left blank", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(200, milestone)));
    renderModal();

    await userEvent.click(await screen.findByRole("button", { name: "Mark as invoiced" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith("/api/v1/projects/milestones/12/status", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ status: "invoiced", revision: 4 }),
      }),
    );
  });

  it("reports a refused move rather than closing on it", async () => {
    stubFetch(() =>
      Promise.resolve(
        jsonResponse(400, {
          title: "Invalid project",
          errors: { status: ["A milestone cannot move from 'planned' to 'invoiced'"] },
        }),
      ),
    );
    const { onClose } = renderModal();

    await userEvent.click(await screen.findByRole("button", { name: "Mark as invoiced" }));

    expect(await screen.findByText("A milestone cannot move from 'planned' to 'invoiced'")).toBeInTheDocument();
    expect(onClose).not.toHaveBeenCalled();
  });

  it("refuses a reference longer than the contract allows before sending it", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(200, milestone)));
    renderModal();

    await userEvent.click(await screen.findByLabelText("Invoice reference"));
    await userEvent.paste("x".repeat(101));
    await userEvent.click(screen.getByRole("button", { name: "Mark as invoiced" }));

    expect(await screen.findByText("An invoice reference is at most 100 characters")).toBeInTheDocument();
    expect(fetchMock.actualCalls.some(([, init]) => init?.method === "POST")).toBe(false);
  });
});
