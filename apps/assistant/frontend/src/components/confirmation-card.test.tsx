import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { renderWithProviders } from "../test/render";
import { card } from "../test/server";
import { ConfirmationCard } from "./confirmation-card";
import "../i18n";

const noop = () => {};

describe("ConfirmationCard", () => {
  it("shows what will happen, the record as a link, and the fields from current to new", async () => {
    const onNavigate = vi.fn();
    renderWithProviders(<ConfirmationCard card={card()} onConfirm={noop} onCancel={noop} onNavigate={onNavigate} />);

    const group = screen.getByRole("group", { name: "Proposed change" });
    expect(within(group).getByText("Set Ola Nordmann's payment terms to 30 days")).toBeInTheDocument();
    expect(within(group).getByText("Update a customer")).toBeInTheDocument();
    const row = within(group).getByRole("row", { name: /paymentTermsDays/ });
    expect(
      within(row)
        .getAllByRole("cell")
        .map((cell) => cell.textContent),
    ).toEqual(["paymentTermsDays", "14", "30"]);
    expect(within(group).getByText(/Nothing is changed until you confirm/)).toBeInTheDocument();

    await userEvent.click(within(group).getByRole("link", { name: "Ola Nordmann" }));
    expect(onNavigate).toHaveBeenCalledWith("/customers/42");
  });

  it("shows the new values alone when no read could say what is there now", () => {
    renderWithProviders(
      <ConfirmationCard
        card={card({ fields: [{ name: "name", next: "Rollout" }], target: undefined })}
        onConfirm={noop}
        onCancel={noop}
      />,
    );
    expect(screen.queryByRole("columnheader", { name: "Now" })).not.toBeInTheDocument();
    expect(screen.getByRole("columnheader", { name: "New value" })).toBeInTheDocument();
    expect(screen.getByRole("cell", { name: "Rollout" })).toBeInTheDocument();
  });

  it("confirms and cancels through the two buttons", async () => {
    const onConfirm = vi.fn();
    const onCancel = vi.fn();
    const pending = card();
    renderWithProviders(<ConfirmationCard card={pending} onConfirm={onConfirm} onCancel={onCancel} />);
    await userEvent.click(screen.getByRole("button", { name: "Confirm" }));
    expect(onConfirm).toHaveBeenCalledWith(pending);
    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(onCancel).toHaveBeenCalledWith(pending);
  });

  it("takes the focus itself when it arrives, never its Confirm button, so Enter confirms nothing", async () => {
    const onConfirm = vi.fn();
    renderWithProviders(<ConfirmationCard card={card()} autoFocus onConfirm={onConfirm} onCancel={noop} />);
    const group = screen.getByRole("group", { name: "Proposed change" });
    expect(group).toHaveFocus();
    expect(screen.getByRole("button", { name: "Confirm" })).not.toHaveFocus();
    await userEvent.keyboard("{Enter}");
    expect(onConfirm).not.toHaveBeenCalled();
  });

  it("disables the buttons while a decision is being made", () => {
    renderWithProviders(<ConfirmationCard card={card()} busy onConfirm={noop} onCancel={noop} />);
    expect(screen.getByRole("button", { name: "Cancel" })).toBeDisabled();
    expect(screen.getByRole("button", { name: /Confirm/ })).toBeDisabled();
  });

  it("shows how a decided card ended, with the reason and no values", () => {
    const decided = (patch: Parameters<typeof card>[0]) =>
      card({ fields: [{ name: "paymentTermsDays" }], decidedAt: "2026-11-02T09:00:00Z", ...patch });

    const { unmount } = renderWithProviders(
      <ConfirmationCard
        card={decided({ status: "confirmed", outcome: { ok: true, status: 200 } })}
        onConfirm={noop}
        onCancel={noop}
      />,
    );
    expect(screen.getByText("Changed")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Confirm" })).not.toBeInTheDocument();
    expect(screen.getByText("Fields: paymentTermsDays")).toBeInTheDocument();
    expect(screen.queryByText("30")).not.toBeInTheDocument();
    unmount();

    for (const [patch, label, reason] of [
      [{ status: "cancelled", outcome: { ok: false, reason: "cancelled" } }, "Cancelled", undefined],
      [
        { status: "expired", outcome: { ok: false, reason: "expired" } },
        "Expired",
        "Nobody confirmed it within 24 hours.",
      ],
      [
        { status: "failed", outcome: { ok: false, status: 412, reason: "changed" } },
        "Failed",
        "The record changed since the card was made, so nothing was written. Ask the assistant again.",
      ],
      [
        {
          status: "failed",
          outcome: { ok: false, status: 403, reason: "refused", message: "You may not edit this customer." },
        },
        "Failed",
        "You may not edit this customer.",
      ],
    ] as const) {
      const view = renderWithProviders(
        <ConfirmationCard card={decided({ ...patch } as never)} onConfirm={noop} onCancel={noop} />,
      );
      expect(screen.getByText(label)).toBeInTheDocument();
      if (reason) expect(screen.getByText(reason)).toBeInTheDocument();
      expect(screen.queryByRole("button", { name: "Confirm" })).not.toBeInTheDocument();
      view.unmount();
    }
  });

  it("says it is making the change between the claim and the outcome", () => {
    renderWithProviders(<ConfirmationCard card={card({ status: "confirmed" })} onConfirm={noop} onCancel={noop} />);
    expect(screen.getByRole("status")).toHaveTextContent("Making the change");
    expect(screen.queryByRole("button", { name: "Confirm" })).not.toBeInTheDocument();
  });

  it("shows a refusal of the decision in words", () => {
    renderWithProviders(
      <ConfirmationCard card={card()} error="Somebody already decided this change." onConfirm={noop} onCancel={noop} />,
    );
    expect(screen.getByRole("alert")).toHaveTextContent("Somebody already decided this change.");
  });
});
