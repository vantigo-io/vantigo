import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { sent } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import { inventoryProblem, locations, stubInventoryApi, transferFixture, transferLine, variants } from "../test/server";

const [tsM, , bolt] = variants();
const rack = (() => {
  const one = locations().find((location) => location.id === 12);
  if (!one) throw new Error("no rack");
  return { id: one.id, siteId: one.siteId, fullCode: one.fullCode, kind: one.kind, status: one.status };
})();

const partly = () =>
  transferFixture({
    id: 7,
    number: 1043,
    status: "partly_received",
    reference: "Weekly restock",
    expectedArrival: "2026-11-12",
    sentAt: "2026-11-08T13:02:00Z",
    sentBy: { displayName: "Kari N." },
    lines: [
      transferLine(1, tsM, 30, { sent: 30, received: 30, fromLocation: rack }),
      transferLine(2, bolt, 12, { sent: 12, received: 10, fromLocation: rack }),
    ],
    actions: [
      {
        id: 1,
        kind: "send",
        movementId: 5531,
        postedAt: "2026-11-08T13:02:00Z",
        postedBy: { displayName: "Kari N." },
        lines: [{ kind: "sent", lineId: 2, quantity: 12, location: rack }],
      },
      {
        id: 2,
        kind: "receive",
        movementId: 5540,
        postedAt: "2026-11-09T08:15:00Z",
        postedBy: { displayName: "Ola H." },
        lines: [{ kind: "received", lineId: 2, quantity: 10 }],
      },
    ],
  });

describe("a transfer's page", () => {
  it("shows each line's sent, received and in transit, and its history with the movements", async () => {
    stubInventoryApi({ transfers: [partly()] });
    renderRoute("/inventory/transfers/7");
    const heading = await screen.findByRole("heading", { name: /Transfer 1043/ });
    expect(within(heading).getByText("Partly received")).toBeInTheDocument();
    expect(within(heading).getByText("In transit 2")).toBeInTheDocument();
    expect(screen.getByText("Weekly restock")).toBeInTheDocument();
    const bolts = screen.getByRole("link", { name: "BOLT-10" }).closest("tr") as HTMLElement;
    expect(within(bolts).getByText("12 pcs")).toBeInTheDocument();
    expect(within(bolts).getByText("10 pcs")).toBeInTheDocument();
    expect(within(bolts).getByText("2 pcs")).toBeInTheDocument();
    expect(screen.getByText("Sent by Kari N. from OSL")).toBeInTheDocument();
    expect(screen.getByText("Received by Ola H. at BGO")).toBeInTheDocument();
    expect(screen.getByText(/10 pcs BOLT-10 received/)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Movement 5540" })).toHaveAttribute("href", "/inventory/movements/5540");
    expect(screen.getByRole("link", { name: "Receive…" })).toHaveAttribute("href", "/inventory/transfers/7/receive");
    expect(screen.getByRole("button", { name: "Print transfer note" })).toBeInTheDocument();
    // Something has arrived, so it can no longer be cancelled (I44).
    expect(screen.queryByRole("button", { name: "Cancel transfer" })).not.toBeInTheDocument();
  });

  it("hides the writes from a caller who may only look, and keeps the transfer note", async () => {
    stubInventoryApi({ transfers: [partly()], meta: { canTransfer: false } });
    renderRoute("/inventory/transfers/7");
    expect(await screen.findByRole("button", { name: "Print transfer note" })).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "Receive…" })).not.toBeInTheDocument();
  });

  it("cancels a sent transfer under its revision with a key of its own", async () => {
    const fetchMock = stubInventoryApi({
      transfers: [
        transferFixture({
          id: 8,
          status: "sent",
          revision: 4,
          lines: [transferLine(1, tsM, 3, { sent: 3, fromLocation: rack })],
        }),
      ],
    });
    renderRoute("/inventory/transfers/8");
    await userEvent.click(await screen.findByRole("button", { name: "Cancel transfer" }));
    const dialog = await screen.findByRole("dialog", { name: "Cancel transfer 1042?" });
    await userEvent.click(within(dialog).getByRole("button", { name: "Cancel transfer" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/transfers\/8\/cancel$/)).toHaveLength(1));
    const [cancel] = sent(fetchMock, "POST", /\/transfers\/8\/cancel$/);
    expect(cancel.ifMatch).toBe("4");
    expect(cancel.body).toEqual({ clientRequestId: expect.stringMatching(/^[0-9a-f-]{36}$/) });
    const heading = await screen.findByRole("heading", { name: /Transfer 1042/ });
    expect(await within(heading).findByText("Cancelled")).toBeInTheDocument();
  });

  it("offers a draft's buttons, and deletes it", async () => {
    const fetchMock = stubInventoryApi({
      transfers: [transferFixture({ id: 9, revision: 2, lines: [transferLine(1, tsM, 3)] })],
    });
    const { router } = renderRoute("/inventory/transfers/9");
    expect(await screen.findByRole("button", { name: "Edit" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Send…" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Send and receive…" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Print transfer note" })).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Delete draft" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByRole("button", { name: "Delete draft" }));
    await waitFor(() => expect(sent(fetchMock, "DELETE", /\/transfers\/9$/)).toHaveLength(1));
    expect(sent(fetchMock, "DELETE", /\/transfers\/9$/)[0].ifMatch).toBe("2");
    await waitFor(() => expect(router.state.location.pathname).toBe("/inventory/transfers"));
  });

  it("says a cancel lost to someone else's change, and reads the transfer again", async () => {
    const fetchMock = stubInventoryApi({
      transfers: [
        transferFixture({ id: 8, status: "sent", lines: [transferLine(1, tsM, 3, { sent: 3, fromLocation: rack })] }),
      ],
      write: (method, path) =>
        method === "POST" && path === "/transfers/8/cancel"
          ? inventoryProblem(409, "revision_conflict", { title: "Revision conflict" })
          : undefined,
    });
    renderRoute("/inventory/transfers/8");
    await userEvent.click(await screen.findByRole("button", { name: "Cancel transfer" }));
    const dialog = await screen.findByRole("dialog", { name: "Cancel transfer 1042?" });
    const reads = sent(fetchMock, "GET", /\/transfers\/8$/).length;
    await userEvent.click(within(dialog).getByRole("button", { name: "Cancel transfer" }));
    expect(await screen.findByText("Changed by someone else")).toBeInTheDocument();
    await waitFor(() => expect(sent(fetchMock, "GET", /\/transfers\/8$/).length).toBeGreaterThan(reads));
  });

  it("says a caller without both views cannot see transfers", async () => {
    const fetchMock = stubInventoryApi({ meta: { canViewLocations: false } });
    renderRoute("/inventory/transfers/8");
    expect(
      await screen.findByText(
        "You cannot see transfers. Ask an administrator for the permissions View locations and View stock.",
      ),
    ).toBeInTheDocument();
    expect(sent(fetchMock, "GET", /\/transfers\/8$/)).toHaveLength(0);
  });

  it("prints the følgeseddel under the name the server gives it", async () => {
    vi.stubGlobal("URL", Object.assign(URL, { createObjectURL: vi.fn(() => "blob:note"), revokeObjectURL: vi.fn() }));
    const names: string[] = [];
    vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function (this: HTMLAnchorElement) {
      names.push(this.download);
    });
    stubInventoryApi({ transfers: [partly()] });
    renderRoute("/inventory/transfers/7");
    await userEvent.click(await screen.findByRole("button", { name: "Print transfer note" }));
    await waitFor(() => expect(names).toEqual(["folgeseddel-TRF-1043.pdf"]));
  });
});
