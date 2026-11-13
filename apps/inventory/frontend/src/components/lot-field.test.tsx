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

/** Where a fixture lot lies: on hand on one of Oslo's racks. */
const at = (locationId: 12 | 13, onHand: number): InventoryLot["locations"][number] => ({
  site: { id: 1, code: "OSL", name: "Oslo lager", kind: "warehouse", status: "active" },
  location: {
    id: locationId,
    fullCode: locationId === 12 ? "OSL-A-01" : "OSL-A-02",
    siteId: 1,
    kind: "rack",
    status: "active",
  },
  onHand,
});

describe("the lot column", () => {
  it("proposes a move's first usable lot on its from location in FEFO order: past the expired one, first received on a tie", async () => {
    const fetchMock = stubInventoryApi({
      variants: [...variants(), milk],
      balances: [
        { variantId: 601, locationId: 12, onHand: 10 },
        { variantId: 601, locationId: 13, onHand: 1 },
      ],
      lots: [
        lot(1001, "E1", { expiry: "2026-09-01", expired: true, locations: [at(12, 1)] }),
        lot(1003, "E3", { locations: [at(12, 2)] }),
        lot(1002, "E2", { expiry: "2026-12-01", receivedAt: "2026-09-05T08:00:00Z", locations: [at(12, 3)] }),
        // On the tie in expiry, the first received goes first, whatever its number.
        lot(1004, "E4", { expiry: "2026-12-01", receivedAt: "2026-08-01T08:00:00Z", locations: [at(12, 4)] }),
        // The earliest expiry, but on another rack: not offered for this one.
        lot(1005, "E0", { expiry: "2026-10-01", locations: [at(13, 1)] }),
      ],
    });
    renderRoute("/inventory/movements/new/move?siteId=1&locationId=12");
    await scan(await scanField(), "MILK-1");
    const table = await screen.findByRole("table");
    const picker = await within(table).findByRole("combobox", { name: "Lot of line 1" });
    await waitFor(() => expect((picker as HTMLInputElement).value).toMatch(/^E4 · /));
    await userEvent.click(picker);
    const options = (await screen.findAllByRole("option")).map((one) => one.textContent);
    expect(options.map((text) => text?.split(" · ")[0])).toEqual(["E1", "E4", "E2", "E3"]);
    await userEvent.keyboard("{Escape}");
    await choose(table, "To, line 1", "OSL-A-02");
    await userEvent.click(screen.getByRole("button", { name: "Post move" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/movements\/moves$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/movements\/moves$/)[0].body.lines).toEqual([
      { variantId: 601, fromLocationId: 12, toLocationId: 13, quantity: 1, lotId: 1004 },
    ]);
  });

  it("proposes no lot before the from location is chosen, and the new location's when it changes", async () => {
    stubInventoryApi({
      variants: [...variants(), milk],
      balances: [
        { variantId: 601, locationId: 12, onHand: 10 },
        { variantId: 601, locationId: 13, onHand: 1 },
      ],
      lots: [lot(1002, "E2", { locations: [at(12, 10)] }), lot(1005, "E5", { locations: [at(13, 1)] })],
    });
    renderRoute("/inventory/movements/new/move?siteId=1");
    await scan(await scanField(), "MILK-1");
    const table = await screen.findByRole("table");
    const picker = (await within(table).findByRole("combobox", { name: "Lot of line 1" })) as HTMLInputElement;
    expect(picker.value).toBe("");
    await choose(table, "From, line 1", "OSL-A-02");
    await waitFor(() => expect(picker.value).toMatch(/^E5 · /));
    await choose(table, "From, line 1", "OSL-A-01");
    await waitFor(() => expect(picker.value).toMatch(/^E2 · /));
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
  it("links a hold an unbuild put to the unbuild, which releases it (manufacturing phase 2 M123)", async () => {
    stubInventoryApi({
      variants: [...variants(), milk],
      lots: [
        lot(1001, "E1", {
          status: "quarantine",
          holds: [
            {
              id: 8,
              kind: "quarantine",
              cause: "recall",
              source: { module: "manufacturing", kind: "unbuild", id: "1002" },
              inventorys: false,
              createdAt: "2026-09-02T08:00:00Z",
            },
          ],
        }),
      ],
    });
    renderRoute("/inventory/lots/1001");
    expect(await screen.findByRole("link", { name: "An unbuild of a held lot" })).toHaveAttribute(
      "href",
      "/manufacturing/unbuilds/1002",
    );
  });

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
    // Block is the block with its downstream list (manufacturing phase 2 M92).
    await userEvent.click(within(dialog).getByRole("button", { name: "Block" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/lots\/1001\/block$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/lots\/1001\/block$/)[0].body).toEqual({
      cause: "recall",
      downstream: true,
      note: "Supplier recall",
    });
    await userEvent.click(await screen.findByRole("button", { name: "Done" }));
    expect(await screen.findAllByText("Blocked")).not.toHaveLength(0);
    // The page holds a lot once per kind: Block waits for this block's release.
    expect(screen.getByRole("button", { name: "Block" })).toBeDisabled();
    expect(
      screen.getByText("This page has blocked the lot already; release that block to put a new one."),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Quarantine" })).toBeEnabled();
    await userEvent.click(await screen.findByRole("button", { name: "Release" }));
    const release = await screen.findByRole("dialog", { name: "Release the hold" });
    await userEvent.type(within(release).getByRole("textbox", { name: "Why it is released" }), "Cleared");
    await userEvent.click(within(release).getByRole("button", { name: "Release" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/holds\/\d+\/release$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/holds\/\d+\/release$/)[0].body).toEqual({ note: "Cleared", downstream: true });
    // Only the deviation's hold is left, and it offers no Release.
    await waitFor(() => expect(screen.queryByRole("button", { name: "Release" })).not.toBeInTheDocument());
    expect(screen.getByRole("button", { name: "Block" })).toBeEnabled();
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

  it("says a page of 200 movement lines is the latest 200, and saves a file rather than opening it", async () => {
    const movements = Array.from({ length: 200 }, (_, i) => ({
      movementId: 5000 - i,
      lineNo: 1,
      kind: "receipt" as const,
      postedAt: "2026-09-01T08:00:00Z",
      quantity: 1,
      to: { id: 12, fullCode: "OSL-A-01", siteId: 1, kind: "rack" as const, status: "active" as const },
    }));
    stubInventoryApi({
      variants: [...variants(), milk],
      lots: [
        lot(1001, "E1", {
          movements,
          attachments: [
            {
              id: 3,
              fileName: "3.1.pdf",
              contentType: "application/pdf",
              sizeBytes: 100,
              uploadedAt: "2026-09-01T08:00:00Z",
            },
          ],
        }),
        lot(1002, "E2", { movements: movements.slice(0, 3) }),
      ],
    });
    renderRoute("/inventory/lots/1001");
    expect(await screen.findByText("The latest 200 movement lines; older ones are in Movements.")).toBeInTheDocument();
    for (const file of screen.getAllByRole("link", { name: "3.1.pdf" })) {
      expect(file).toHaveAttribute("download", "3.1.pdf");
      expect(file).not.toHaveAttribute("target");
    }
  });

  it("says nothing of the latest when fewer lines come back", async () => {
    stubInventoryApi({ variants: [...variants(), milk], lots: [lot(1002, "E2")] });
    renderRoute("/inventory/lots/1002");
    expect(await screen.findByText("No movements")).toBeInTheDocument();
    expect(screen.queryByText(/The latest 200/)).not.toBeInTheDocument();
  });
});
