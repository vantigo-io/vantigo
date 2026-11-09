import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { choose, scan, sent } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import { type Placement, stubInventoryApi, transferFixture, transferLine, variants } from "../test/server";

const [tsM, , bolt] = variants();
const placement = (variantId: number, siteId: number, primaryLocationId: number): Placement => ({
  variantId,
  siteId,
  primaryLocationId,
  reorderPoint: null,
  maximum: null,
  revision: 1,
});

/** The primary location is the default, never a rule (I55). */
describe("defaults from the primary location (I55)", () => {
  it("puts a receipt line on the variant's primary location, picked or scanned", async () => {
    const fetchMock = stubInventoryApi({ placements: [placement(101, 1, 13), placement(201, 2, 21)] });
    renderRoute("/inventory/movements/new/receipt?siteId=1&locationId=12");
    await scan(await screen.findByRole("textbox", { name: "Scan an item" }), "TS-M");
    await waitFor(() => expect(screen.getByRole("combobox", { name: "Location, line 1" })).toHaveValue("OSL-A-02"));
    // The bolt's primary location is at Bergen, not here: its line goes where the receipt does.
    await userEvent.click(screen.getByRole("button", { name: "Add line" }));
    await userEvent.type(screen.getByRole("combobox", { name: "Item, line 2" }), "BOLT");
    await userEvent.click(await screen.findByRole("option", { name: "BOLT-10 · Bolt" }));
    expect(screen.getByRole("combobox", { name: "Location, line 2" })).toHaveValue("");
    await userEvent.type(screen.getByRole("textbox", { name: "Quantity, line 2" }), "1");
    expect(sent(fetchMock, "GET", /\/variants\?/).every((one) => one.url.includes("siteId=1"))).toBe(true);

    await userEvent.click(screen.getByRole("button", { name: "Post receipt" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/movements\/receipts$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/movements\/receipts$/)[0].body.lines).toEqual([
      { variantId: 101, quantity: 1, locationId: 13 },
      { variantId: 201, quantity: 1 },
    ]);
  });

  it("sends from the primary location when it holds some, else from the location holding most", async () => {
    const fetchMock = stubInventoryApi({
      transfers: [transferFixture({ id: 5, revision: 2, lines: [transferLine(1, tsM, 2), transferLine(2, bolt, 2)] })],
      balances: [
        { variantId: 101, locationId: 12, onHand: 2 },
        { variantId: 101, locationId: 13, onHand: 6 },
        { variantId: 201, locationId: 12, onHand: 9 },
      ],
      // TS-M's primary holds less than the other; the bolt's primary holds nothing.
      placements: [placement(101, 1, 12), placement(201, 1, 13), placement(101, 2, 21)],
    });
    renderRoute("/inventory/transfers/5");
    await userEvent.click(await screen.findByRole("button", { name: "Send and receive…" }));
    const dialog = await screen.findByRole("dialog");
    await waitFor(() =>
      expect(within(dialog).getByRole("combobox", { name: "Source location, line 1" })).toHaveValue(
        "OSL-A-01 (primary) · 2",
      ),
    );
    expect(within(dialog).getByRole("combobox", { name: "Source location, line 2" })).toHaveValue("OSL-A-01 · 9");
    // Received at the to site's primary location, where there is one.
    expect(within(dialog).getByRole("combobox", { name: "Destination, line 1" })).toHaveValue("BGO-SALG · Butikkgulv");
    await choose(dialog, "Destination, line 2", "BGO");
    await userEvent.click(within(dialog).getByRole("button", { name: "Send and receive 2 lines" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/transfers\/5\/send$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/transfers\/5\/send$/)[0].body).toEqual({
      lines: [
        { lineId: 1, fromLocationId: 12 },
        { lineId: 2, fromLocationId: 12 },
      ],
      receive: {
        lines: [
          { lineId: 1, toLocationId: 21 },
          { lineId: 2, toLocationId: 20 },
        ],
      },
    });
  });

  it("receives a line at its primary location at the to site, else at the site's root", async () => {
    const fetchMock = stubInventoryApi({
      transfers: [
        transferFixture({
          id: 7,
          number: 1043,
          status: "sent",
          lines: [transferLine(1, tsM, 3, { sent: 3 }), transferLine(2, bolt, 2, { sent: 2 })],
        }),
      ],
      placements: [placement(101, 2, 21)],
    });
    renderRoute("/inventory/transfers/7/receive");
    await waitFor(() =>
      expect(screen.getByRole("combobox", { name: "To location, TS-M 1" })).toHaveValue("BGO-SALG · Butikkgulv"),
    );
    expect(screen.getByRole("combobox", { name: "To location, BOLT-10 2" })).toHaveValue("BGO");
    await userEvent.click(screen.getByRole("button", { name: "Accept all" }));
    await userEvent.click(screen.getByRole("button", { name: "Receive" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/transfers\/7\/receive$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/transfers\/7\/receive$/)[0].body.lines).toEqual([
      { lineId: 1, toLocationId: 21, quantity: 3 },
      { lineId: 2, toLocationId: 20, quantity: 2 },
    ]);
  });
});
