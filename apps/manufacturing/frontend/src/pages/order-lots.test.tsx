import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { ManufacturingProductionDraft } from "../api/orders";
import { jsonResponse } from "../test/api";
import { sent } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import { stubManufacturingApi } from "../test/server";

// Lots in Manufacturing (manufacturing phase 2 M80–M83): the production
// dialog's output lot and the components' lots, the order's Lots tab and the
// BoM's lot number pattern.

const PRODUCTION = { id: 70, orderId: 5, orderNumber: "MO-12", quantity: "10", outputScrap: "0", lines: [] };

/** The draft of a lot-tracked table whose boards are tracked too: two lots proposed, a third offered. */
const trackedDraft = (draft: ManufacturingProductionDraft): ManufacturingProductionDraft => ({
  ...draft,
  output: {
    ...draft.output,
    tracking: "lot",
    lotNumber: "MO-12",
    expiry: "2026-12-11",
  },
  lines: draft.lines.map((line) => ({
    ...line,
    tracking: "lot",
    lots: [
      { lotId: 501, lotNumber: "B-OLD", expiry: "2026-12-01", locationId: 11, fullCode: "OSL-A1", quantity: "30" },
      { lotId: 502, lotNumber: "B-NEW", expiry: "2027-01-01", locationId: 11, fullCode: "OSL-A1", quantity: "10" },
    ],
    lotChoices: [
      {
        lotId: 501,
        lotNumber: "B-OLD",
        expiry: "2026-12-01",
        bestBefore: null,
        locationId: 11,
        fullCode: "OSL-A1",
        quantity: "30",
        status: "available",
        expired: false,
        serial: false,
      },
      {
        lotId: 502,
        lotNumber: "B-NEW",
        expiry: "2027-01-01",
        bestBefore: null,
        locationId: 11,
        fullCode: "OSL-A1",
        quantity: "50",
        status: "available",
        expired: false,
        serial: false,
      },
      {
        lotId: 503,
        lotNumber: "B-HELD",
        expiry: null,
        bestBefore: null,
        locationId: 11,
        fullCode: "OSL-A1",
        quantity: "5",
        status: "quarantine",
        expired: false,
        serial: false,
      },
    ],
  })),
});

const openDialog = async () => {
  renderRoute("/manufacturing/orders/5");
  await userEvent.click(await screen.findByRole("button", { name: "Register production" }));
  return screen.findByRole("dialog");
};

describe("the production dialog's lots", () => {
  it("shows the proposal and leaves the lots to Inventory when they are not changed", async () => {
    const fetchMock = stubManufacturingApi({
      meta: { canWorkShopFloor: true },
      draft: trackedDraft,
      write: (method, path) =>
        method === "POST" && path === "/orders/5/productions" ? jsonResponse(201, PRODUCTION) : undefined,
    });
    const dialog = await openDialog();
    expect(await within(dialog).findByText(/Taken first-expiry-first when you post: B-OLD/)).toBeInTheDocument();
    expect(within(dialog).getByLabelText(/Lot number/)).toHaveValue("MO-12");
    await waitFor(() => expect(within(dialog).getByLabelText("Consumed of BOARD")).toHaveValue("40.000"));
    await userEvent.click(within(dialog).getByRole("button", { name: "Post" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/orders\/5\/productions$/)).toHaveLength(1));
    const [{ body }] = sent(fetchMock, "POST", /\/orders\/5\/productions$/);
    expect(body).not.toHaveProperty("outputLot");
    expect((body.lines as Record<string, unknown>[])[0]).not.toHaveProperty("lots");
  });

  it("sends the lots a person chose, consumed first, and the lot number typed", async () => {
    const fetchMock = stubManufacturingApi({
      meta: { canWorkShopFloor: true },
      draft: trackedDraft,
      write: (method, path) =>
        method === "POST" && path === "/orders/5/productions" ? jsonResponse(201, PRODUCTION) : undefined,
    });
    const dialog = await openDialog();
    await waitFor(() => expect(within(dialog).getByLabelText("Consumed of BOARD")).toHaveValue("40.000"));
    await userEvent.type(within(dialog).getByLabelText("Scrap of BOARD"), "2");
    await userEvent.click(within(dialog).getByRole("button", { name: "Change lot" }));
    // The proposal's 30 + 10 no longer covers 40 consumed and 2 scrapped.
    expect(await within(dialog).findByText("The lots take 40; the line consumes and scraps 42.")).toBeInTheDocument();
    expect(within(dialog).getByRole("button", { name: "Post" })).toBeDisabled();
    const second = within(dialog).getByLabelText("Quantity of lot 2 of BOARD");
    await userEvent.clear(second);
    await userEvent.type(second, "12");
    const number = within(dialog).getByLabelText(/Lot number/);
    await userEvent.clear(number);
    await userEvent.type(number, "JAM-7");
    await userEvent.click(within(dialog).getByRole("button", { name: "Post" }));

    await waitFor(() => expect(sent(fetchMock, "POST", /\/orders\/5\/productions$/)).toHaveLength(1));
    const [{ body }] = sent(fetchMock, "POST", /\/orders\/5\/productions$/);
    expect(body).toMatchObject({
      outputLot: { number: "JAM-7" },
      lines: [
        {
          componentId: 60,
          consumed: "40.000",
          scrap: "2",
          lots: [
            { lotId: 501, locationId: 11, consumed: "30", scrapped: "0" },
            { lotId: 502, locationId: 11, consumed: "10", scrapped: "2" },
          ],
        },
      ],
    });
  });

  it("proposes the lots again at the location a person chose for the line", async () => {
    const fetchMock = stubManufacturingApi({
      meta: { canWorkShopFloor: true },
      draft: (draft) => {
        const tracked = trackedDraft(draft);
        return {
          ...tracked,
          lines: tracked.lines.map((line) => ({
            ...line,
            lotChoices: [
              ...line.lotChoices,
              {
                lotId: 504,
                lotNumber: "B-SITE",
                expiry: "2027-03-01",
                bestBefore: null,
                locationId: 10,
                fullCode: "OSL",
                quantity: "100",
                status: "available" as const,
                expired: false,
                serial: false,
              },
            ],
          })),
        };
      },
      write: (method, path) =>
        method === "POST" && path === "/orders/5/productions" ? jsonResponse(201, PRODUCTION) : undefined,
    });
    const dialog = await openDialog();
    expect(await within(dialog).findByText(/Taken first-expiry-first when you post: B-OLD/)).toBeInTheDocument();
    await waitFor(() => expect(within(dialog).getByLabelText("Consumed of BOARD")).toHaveValue("40.000"));
    await userEvent.click(within(dialog).getByLabelText("Take BOARD from", { selector: "input" }));
    await userEvent.click(await screen.findByRole("option", { name: "OSL" }));
    expect(await within(dialog).findByText(/Taken first-expiry-first when you post: B-SITE/)).toBeInTheDocument();
    expect(within(dialog).queryByText(/B-OLD/)).not.toBeInTheDocument();
    // Change lot starts from the proposal shown.
    await userEvent.click(within(dialog).getByRole("button", { name: "Change lot" }));
    await userEvent.click(within(dialog).getByRole("button", { name: "Post" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/orders\/5\/productions$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/orders\/5\/productions$/)[0].body).toMatchObject({
      lines: [{ componentId: 60, fromLocationId: 10, lots: [{ lotId: 504, locationId: 10, consumed: "40" }] }],
    });
  });

  it("refuses a lot row of 0 and a lot chosen twice before posting", async () => {
    stubManufacturingApi({ meta: { canWorkShopFloor: true }, draft: trackedDraft });
    const dialog = await openDialog();
    await waitFor(() => expect(within(dialog).getByLabelText("Consumed of BOARD")).toHaveValue("40.000"));
    await userEvent.click(within(dialog).getByRole("button", { name: "Change lot" }));
    await userEvent.click(within(dialog).getByRole("button", { name: "Add lot" }));
    await userEvent.type(within(dialog).getByLabelText("Quantity of lot 3 of BOARD"), "0");
    expect(await within(dialog).findByText("More than 0")).toBeInTheDocument();
    await userEvent.click(within(dialog).getByLabelText("Lot 3 of BOARD", { selector: "input" }));
    await userEvent.click(await screen.findByRole("option", { name: /^B-OLD/ }));
    expect(await within(dialog).findByText("Chosen in another row")).toBeInTheDocument();
    expect(within(dialog).getByRole("button", { name: "Post" })).toBeDisabled();
  });

  it("asks a serial product's first serial number", async () => {
    stubManufacturingApi({
      meta: { canWorkShopFloor: true },
      draft: (draft) => ({ ...draft, output: { ...draft.output, tracking: "serial", serialFirst: "MO-12-0001" } }),
    });
    const dialog = await openDialog();
    expect(await within(dialog).findByLabelText(/First serial number/)).toHaveValue("MO-12-0001");
  });
});

describe("the order's Lots tab", () => {
  it("lists the lots made and consumed, each linked to Inventory", async () => {
    stubManufacturingApi({
      meta: { canTraceLots: true },
      orderLots: {
        5: {
          byProducts: [],
          outputs: [{ lotId: 900, lotNumber: "MO-12", serial: false, quantity: "6", productions: 2, unbuilt: "0" }],
          components: [
            {
              componentId: 60,
              variant: {
                id: 2,
                sku: "BOARD",
                productName: "Board",
                unit: "pcs",
                productType: "Goods",
                productStatus: "Active",
              },
              lots: [{ lotId: 501, lotNumber: "B-OLD", expiry: "2026-12-01", consumed: "24", scrapped: "1" }],
            },
          ],
        },
      },
    });
    const { router } = renderRoute("/manufacturing/orders/5");
    await userEvent.click(await screen.findByRole("tab", { name: "Lots" }));
    expect(await screen.findByRole("link", { name: "MO-12" })).toHaveAttribute("href", "/inventory/lots/900");
    expect(screen.getByRole("link", { name: "B-OLD" })).toHaveAttribute("href", "/inventory/lots/501");
    expect(screen.getByText("Lots made")).toBeInTheDocument();
    // Each lot links its trace (manufacturing phase 2 M94), on where it went.
    expect(screen.getByRole("link", { name: "Trace lot MO-12" })).toHaveAttribute("href", "/inventory/trace?lotId=900");
    expect(screen.getByRole("link", { name: "Trace lot B-OLD" })).toHaveAttribute("href", "/inventory/trace?lotId=501");
    // The links go through the router, not a reload.
    await userEvent.click(screen.getByRole("link", { name: "B-OLD" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/inventory/lots/501"));
  });

  it("shows the lots without links to a caller who may not open them in Inventory", async () => {
    stubManufacturingApi({
      meta: { canTraceLots: false },
      orderLots: {
        5: {
          byProducts: [],
          outputs: [{ lotId: 900, lotNumber: "MO-12", serial: false, quantity: "6", productions: 2, unbuilt: "0" }],
          components: [],
        },
      },
    });
    renderRoute("/manufacturing/orders/5");
    await userEvent.click(await screen.findByRole("tab", { name: "Lots" }));
    expect(await screen.findByText("Lots made")).toBeInTheDocument();
    expect(screen.getAllByText("MO-12").length).toBeGreaterThan(0);
    expect(screen.queryByRole("link", { name: "MO-12" })).not.toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "Trace lot MO-12" })).not.toBeInTheDocument();
  });

  it("says when no lot was made or consumed", async () => {
    stubManufacturingApi();
    renderRoute("/manufacturing/orders/5");
    await userEvent.click(await screen.findByRole("tab", { name: "Lots" }));
    expect(await screen.findByText("No production has made or consumed a lot yet.")).toBeInTheDocument();
  });
});

describe("the work order's lots", () => {
  it("shows the order's lots on the shop floor's work order", async () => {
    stubManufacturingApi({
      meta: { canWorkShopFloor: true, canTraceLots: true },
      orderLots: {
        5: {
          byProducts: [],
          outputs: [{ lotId: 900, lotNumber: "MO-12", serial: false, quantity: "6", productions: 1, unbuilt: "0" }],
          components: [],
        },
      },
    });
    renderRoute("/manufacturing/shop-floor/work-orders/40");
    expect(await screen.findByText("Lots made")).toBeInTheDocument();
    const hrefs = screen.getAllByRole("link", { name: "MO-12" }).map((link) => link.getAttribute("href"));
    expect(hrefs).toContain("/inventory/lots/900");
  });
});

describe("the BoM's lot number pattern", () => {
  it("previews the pattern, refuses one that gives no lot number and saves it", async () => {
    const fetchMock = stubManufacturingApi();
    renderRoute("/manufacturing/boms/7");
    await screen.findByDisplayValue("Cut");
    const pattern = screen.getByLabelText(/Lot number pattern/);
    expect(pattern).toHaveValue("{MO}");
    expect(screen.getByText(/For example MO-1001/)).toBeInTheDocument();
    await userEvent.clear(pattern);
    await userEvent.type(pattern, "L{{N} X");
    expect(await screen.findByText(/a lot number has 1–20 letters/)).toBeInTheDocument();
    // A pattern whose preview fits, but a busy shop's order numbers outgrow.
    await userEvent.clear(pattern);
    await userEvent.type(pattern, "LOTSX-{{MO}-{{N}");
    expect(await screen.findByText(/Can render LOTSX-MO-99999-9-9999/)).toBeInTheDocument();
    await userEvent.clear(pattern);
    await userEvent.type(pattern, "L-{{N}");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(sent(fetchMock, "PUT", /\/boms\/7$/)).toHaveLength(1));
    expect(sent(fetchMock, "PUT", /\/boms\/7$/)[0].body).toMatchObject({ lotPattern: "L-{N}" });
  });
});
