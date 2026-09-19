import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { BillingMilestone } from "../api/milestones";
import { stubFetch } from "../test/fetch";
import { renderWithProviders } from "../test/render";
import { MilestoneFormModal, type MilestoneModalState } from "./-milestone-form-modal";

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const money = (amount: number, currency = "NOK") =>
  new Intl.NumberFormat("en-US", { style: "currency", currency }).format(amount).replace(/\u00a0/g, " ");

const milestone: BillingMilestone = {
  id: 12,
  projectId: 7,
  name: "Kick-off",
  description: "The first invoice",
  plannedDate: "2026-03-31",
  percent: 30,
  effectiveAmount: 300000,
  currency: "NOK",
  status: "planned",
  position: 1,
  overdue: false,
  revision: 4,
  createdAt: "2026-01-01T10:00:00Z",
  updatedAt: "2026-01-01T10:00:00Z",
  capabilities: {
    canEdit: true,
    canDelete: true,
    canMarkReady: true,
    canMarkPlanned: false,
    canMarkInvoiced: false,
    canUndoInvoiced: false,
    canCancel: true,
    canReopen: false,
  },
} as BillingMilestone;

const stubSave = (response: () => Response) => stubFetch(() => Promise.resolve(response()));

const renderModal = (
  state: MilestoneModalState | null,
  { fixedPrice, currency = "NOK" }: { fixedPrice?: number; currency?: string } = { fixedPrice: 1000000 },
) => {
  const onClose = vi.fn();
  renderWithProviders(
    <MilestoneFormModal projectId={7} fixedPrice={fixedPrice} currency={currency} state={state} onClose={onClose} />,
  );
  return { onClose };
};

/** The numeric field, whether Mantine hung the test id on the input or its wrapper. */
const numberInput = (testId: string) => {
  const field = screen.getByTestId(testId);
  return (field.tagName === "INPUT" ? field : within(field).getByRole("textbox")) as HTMLInputElement;
};

describe("MilestoneFormModal", () => {
  it("creates a milestone priced at a flat amount", async () => {
    const fetchMock = stubSave(() => jsonResponse(201, { ...milestone, name: "Kick-off" }));
    const { onClose } = renderModal({ mode: "create" }, { fixedPrice: 1000000 });

    await userEvent.type(await screen.findByLabelText(/^Name/), "Kick-off");
    await userEvent.type(numberInput("milestone-amount"), "300000");
    await userEvent.click(screen.getByRole("button", { name: "Create" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith("/api/v1/projects/7/milestones", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ name: "Kick-off", amount: 300000 }),
      }),
    );
    expect(onClose).toHaveBeenCalled();
  });

  it("cannot price a milestone as a share until the project has a fixed price", async () => {
    renderModal({ mode: "create" }, { fixedPrice: undefined });

    const share = await screen.findByRole("radio", { name: "A share of the fixed price" });
    expect(share).toBeDisabled();
    expect(screen.getByText("A share needs the project to have a fixed price")).toBeInTheDocument();
  });

  it("previews what a share comes to, said as an approximation", async () => {
    renderModal({ mode: "create" }, { fixedPrice: 1000000 });

    await userEvent.click(await screen.findByRole("radio", { name: "A share of the fixed price" }));
    await userEvent.type(numberInput("milestone-percent"), "30");

    expect(await screen.findByText(`About ${money(300000)} of the fixed price`)).toBeInTheDocument();
  });

  it("refuses an amount of zero before the API has to", async () => {
    const fetchMock = stubSave(() => jsonResponse(201, milestone));
    renderModal({ mode: "create" }, { fixedPrice: 1000000 });

    await userEvent.type(await screen.findByLabelText(/^Name/), "Kick-off");
    await userEvent.type(numberInput("milestone-amount"), "0");
    await userEvent.click(screen.getByRole("button", { name: "Create" }));

    expect(await screen.findByText("A milestone needs an amount above 0")).toBeInTheDocument();
    expect(fetchMock.actualCalls.some(([, init]) => init?.method === "POST")).toBe(false);
  });

  it("names the ceiling an amount is over, rather than calling it zero", async () => {
    const fetchMock = stubSave(() => jsonResponse(201, milestone));
    renderModal({ mode: "create" }, { fixedPrice: 1000000 });

    await userEvent.type(await screen.findByLabelText(/^Name/), "Kick-off");
    await userEvent.type(numberInput("milestone-amount"), "99999999999");
    await userEvent.click(screen.getByRole("button", { name: "Create" }));

    expect(await screen.findByText("A milestone amount is at most 9 999 999 999.99")).toBeInTheDocument();
    expect(fetchMock.actualCalls.some(([, init]) => init?.method === "POST")).toBe(false);
  });

  it("edits a milestone as a full replace carrying the revision it was read at", async () => {
    const fetchMock = stubSave(() => jsonResponse(200, milestone));
    renderModal({ mode: "edit", milestone }, { fixedPrice: 1000000 });

    const share = await screen.findByRole("radio", { name: "A share of the fixed price" });
    expect(share).toBeChecked();
    expect(numberInput("milestone-percent")).toHaveValue("30");

    await userEvent.click(screen.getByRole("button", { name: "Save changes" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith("/api/v1/projects/milestones/12", {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          name: "Kick-off",
          description: "The first invoice",
          plannedDate: "2026-03-31",
          percent: 30,
          revision: 4,
        }),
      }),
    );
  });

  it("puts the 'exactly one of amount and percent' answer where the caller is looking", async () => {
    stubSave(() =>
      jsonResponse(400, {
        title: "Invalid project",
        errors: {
          amount: ["Exactly one of amount and percent is required"],
          percent: ["Exactly one of amount and percent is required"],
        },
      }),
    );
    renderModal({ mode: "create" }, { fixedPrice: 1000000 });

    await userEvent.type(await screen.findByLabelText(/^Name/), "Kick-off");
    await userEvent.type(numberInput("milestone-amount"), "300000");
    await userEvent.click(screen.getByRole("button", { name: "Create" }));

    expect(await screen.findByText("Exactly one of amount and percent is required")).toBeInTheDocument();
  });

  it("says out loud that a read-only milestone cannot be edited", async () => {
    stubSave(() =>
      jsonResponse(400, {
        title: "Invalid project",
        errors: { status: ["An invoiced milestone cannot be edited; undo the invoicing first"] },
      }),
    );
    renderModal({ mode: "edit", milestone }, { fixedPrice: 1000000 });

    await userEvent.click(await screen.findByRole("button", { name: "Save changes" }));

    expect(
      await screen.findByText("An invoiced milestone cannot be edited; undo the invoicing first"),
    ).toBeInTheDocument();
  });

  it("translates a revision conflict instead of quoting the project's wording", async () => {
    stubSave(() =>
      jsonResponse(409, {
        title: "Project revision conflict",
        detail: "The project has revision 9; the supplied revision was 4.",
      }),
    );
    renderModal({ mode: "edit", milestone }, { fixedPrice: 1000000 });

    await userEvent.click(await screen.findByRole("button", { name: "Save changes" }));

    expect(
      await screen.findByText("The milestone was changed by someone else. Reload the plan and try again."),
    ).toBeInTheDocument();
  });
});
