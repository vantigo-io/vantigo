import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { InventoryLot } from "../api/lots";
import type { InventoryVariantRef } from "../api/stock";
import { choose, scan, sent } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import { stubInventoryApi, variants } from "../test/server";

// The lot column (manufacturing phase 2 M59): tracked variants are made in
// the fake's catalog, as Products' guard keeps them out until layer M.
const milk: InventoryVariantRef = {
  id: 601,
  productId: 6,
  sku: "MILK-1",
  productName: "Milk",
  unit: "l",
  tracking: "lot",
};
const pump: InventoryVariantRef = {
  id: 602,
  productId: 7,
  sku: "PUMP-1",
  productName: "Pump",
  unit: "pcs",
  tracking: "serial",
};

const lot = (id: number, number: string, fields: Partial<InventoryLot> = {}): InventoryLot => ({
  id,
  number,
  serial: false,
  variant: milk,
  status: "available",
  expired: false,
  receivedAt: "2026-09-01T08:00:00Z",
  onHand: 5,
  revision: 1,
  createdAt: "2026-09-01T08:00:00Z",
  holds: [],
  locations: [],
  movements: [],
  attachments: [],
  ...fields,
});

const scanField = () => screen.findByRole("textbox", { name: "Scan an item" });

describe("the lot column", () => {
  it("proposes a move's first usable lot in FEFO order, past the expired one", async () => {
    const fetchMock = stubInventoryApi({
      variants: [...variants(), milk],
      balances: [{ variantId: 601, locationId: 12, onHand: 10 }],
      lots: [
        lot(1001, "E1", { expiry: "2026-09-01", expired: true }),
        lot(1003, "E3"),
        lot(1002, "E2", { expiry: "2026-12-01" }),
      ],
    });
    renderRoute("/inventory/movements/new/move?siteId=1&locationId=12");
    await scan(await scanField(), "MILK-1");
    const table = await screen.findByRole("table");
    const picker = await within(table).findByRole("combobox", { name: "Lot of line 1" });
    await waitFor(() => expect((picker as HTMLInputElement).value).toMatch(/^E2 · /));
    await choose(table, "To, line 1", "OSL-A-02");
    await userEvent.click(screen.getByRole("button", { name: "Post move" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/movements\/moves$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/movements\/moves$/)[0].body.lines).toEqual([
      { variantId: 601, fromLocationId: 12, toLocationId: 13, quantity: 1, lotId: 1002 },
    ]);
  });

  it("names a receipt's lot by the number typed, and a serial variant's by its first serial and count", async () => {
    const fetchMock = stubInventoryApi({ variants: [...variants(), milk, pump] });
    renderRoute("/inventory/movements/new/receipt?siteId=1&locationId=12");
    const field = await scanField();
    await scan(field, "MILK-1");
    await scan(field, "PUMP-1");
    const table = await screen.findByRole("table");
    await userEvent.type(await within(table).findByRole("combobox", { name: "Lot of line 1" }), "A-7");
    await userEvent.type(within(table).getByRole("textbox", { name: "Serial numbers of line 2" }), "SN0099");
    const count = within(table).getByRole("textbox", { name: "How many" });
    await userEvent.clear(count);
    await userEvent.type(count, "3");
    await waitFor(() => expect(within(table).getByRole("textbox", { name: "Quantity, line 2" })).toHaveValue("3"));
    await userEvent.click(screen.getByRole("button", { name: "Post receipt" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/movements\/receipts$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/movements\/receipts$/)[0].body.lines).toEqual([
      { variantId: 601, quantity: 1, lot: { number: "A-7" } },
      { variantId: 602, quantity: 3, serials: { first: "SN0099", count: 3 } },
    ]);
  });

  it("holds a move whose tracked line names no lot", async () => {
    stubInventoryApi({ variants: [...variants(), milk], balances: [{ variantId: 601, locationId: 12, onHand: 10 }] });
    renderRoute("/inventory/movements/new/move?siteId=1&locationId=12");
    await scan(await scanField(), "MILK-1");
    const table = await screen.findByRole("table");
    await choose(table, "To, line 1", "OSL-A-02");
    await userEvent.click(screen.getByRole("button", { name: "Post move" }));
    expect(await screen.findByText("Pick the line's lot")).toBeInTheDocument();
  });
});

describe("the lot page", () => {
  it("puts a quarantine, releases Inventory's hold, and leaves another module's to it", async () => {
    const fetchMock = stubInventoryApi({
      variants: [...variants(), milk],
      lots: [
        lot(1001, "E1", {
          status: "quarantine",
          holds: [
            {
              id: 7,
              kind: "quarantine",
              cause: "deviation",
              source: { module: "manufacturing", kind: "deviation", id: "4" },
              inventorys: false,
              createdAt: "2026-09-02T08:00:00Z",
            },
          ],
        }),
      ],
    });
    renderRoute("/inventory/lots/1001");
    expect(await screen.findByText("Put by manufacturing, released there")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Block" }));
    const dialog = await screen.findByRole("dialog", { name: "Block the lot" });
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Note" }), "Supplier recall");
    await userEvent.click(within(dialog).getByRole("button", { name: "Put on hold" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/lots\/1001\/holds$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/lots\/1001\/holds$/)[0].body).toEqual({
      kind: "block",
      cause: "recall",
      note: "Supplier recall",
    });
    expect(await screen.findAllByText("Blocked")).not.toHaveLength(0);
    await userEvent.click(await screen.findByRole("button", { name: "Release" }));
    const release = await screen.findByRole("dialog", { name: "Release the hold" });
    await userEvent.type(within(release).getByRole("textbox", { name: "Why it is released" }), "Cleared");
    await userEvent.click(within(release).getByRole("button", { name: "Release" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/holds\/\d+\/release$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/holds\/\d+\/release$/)[0].body).toEqual({ note: "Cleared" });
    // Only the deviation's hold is left, and it offers no Release.
    await waitFor(() => expect(screen.queryByRole("button", { name: "Release" })).not.toBeInTheDocument());
  });

  it("offers no writes without lots-manage", async () => {
    stubInventoryApi({ meta: { canManageLots: false }, variants: [...variants(), milk], lots: [lot(1001, "E1")] });
    renderRoute("/inventory/lots/1001");
    expect(await screen.findByText("No holds. The lot can be delivered and used.")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Quarantine" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Edit" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Add file" })).not.toBeInTheDocument();
  });

  it("lists the lots with their status", async () => {
    stubInventoryApi({
      variants: [...variants(), milk],
      lots: [lot(1001, "E1", { expiry: "2026-09-01", expired: true }), lot(1002, "E2", { status: "blocked" })],
    });
    renderRoute("/inventory/lots");
    expect(await screen.findByRole("link", { name: "E1" })).toHaveAttribute("href", "/inventory/lots/1001");
    const table = screen.getByRole("table");
    expect(within(table).getByText("Expired")).toBeInTheDocument();
    expect(within(table).getByText("Blocked")).toBeInTheDocument();
  });
});
