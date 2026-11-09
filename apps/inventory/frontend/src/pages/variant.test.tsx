import { screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { InventoryMovement } from "../api/movements";
import { renderRoute } from "../test/route-tree";
import { stubInventoryApi, transferFixture, transferLine, variants } from "../test/server";

const bolt = variants()[2];

describe("a variant's stock page and transfers", () => {
  it("shows what is incoming per site, the transfer differences, and links a transfer's movement", async () => {
    const movement: InventoryMovement = {
      id: 77,
      kind: "transfer_out",
      clientRequestId: "x",
      site: { id: 1, code: "OSL", name: "Oslo lager", kind: "warehouse", status: "active" },
      postedAt: "2026-11-08T13:02:00Z",
      postedBy: { displayName: "Kari N." },
      transfer: { id: 3, number: 1003 },
      lines: [{ lineNo: 1, variant: bolt, quantity: 5 }],
    };
    stubInventoryApi({
      balances: [{ variantId: 201, locationId: 12, onHand: 5 }],
      movements: [movement],
      transfers: [
        transferFixture({ id: 3, number: 1003, status: "sent", lines: [transferLine(1, bolt, 5, { sent: 5 })] }),
        transferFixture({
          id: 4,
          number: 1004,
          status: "received",
          closeReason: "lost",
          lines: [transferLine(2, bolt, 4, { sent: 4, received: 3, missing: 1 })],
        }),
      ],
    });
    renderRoute("/inventory/variants/201");
    expect(await screen.findByText("+5 pcs incoming")).toBeInTheDocument();
    expect(screen.getByText("BGO · Bergen butikk")).toBeInTheDocument();
    // The movements name the kind and link the transfer.
    const row = (await screen.findByRole("link", { name: "Movement 77" })).closest("tr") as HTMLElement;
    expect(within(row).getByText("Transfer out")).toBeInTheDocument();
    expect(within(row).getByRole("link", { name: "Transfer 1003" })).toHaveAttribute("href", "/inventory/transfers/3");
    // Only the transfer whose difference was closed is a difference (I46).
    const differences = (await screen.findByRole("link", { name: "Transfer 1004" })).closest("tr") as HTMLElement;
    expect(within(differences).getByText("1")).toBeInTheDocument();
    expect(screen.getAllByRole("link", { name: "Transfer 1003" })).toHaveLength(1);
  });
});
