import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { choose, retype, scan, sent } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import { packagings, stubInventoryApi, variants } from "../test/server";

/** The catalog with a variant stocked in Euro pallets of 600 and boxes of 15. */
const catalog = () => [
  ...variants(),
  { id: 301, productId: 3, sku: "PAL-1", productName: "Pallet goods", unit: "pcs", packagings: packagings() },
];
const PALLET_GTIN = "17070000000201";
const BOX_GTIN = "27070000000201";

describe("quantities in packagings (I25)", () => {
  it("adds one packaging per scan of its barcode, steps the others, and posts entered", async () => {
    const fetchMock = stubInventoryApi({ variants: catalog() });
    renderRoute("/inventory/movements/new/receipt?siteId=1&locationId=12");
    const field = await screen.findByRole("textbox", { name: "Scan an item" });
    await scan(field, PALLET_GTIN);
    expect(await screen.findByRole("textbox", { name: "Euro pallet, line 1" })).toHaveValue("1");
    await scan(field, PALLET_GTIN);
    await waitFor(() => expect(screen.getByRole("textbox", { name: "Euro pallet, line 1" })).toHaveValue("2"));
    // The box's barcode counts a box on the same line, not a new one.
    await scan(field, BOX_GTIN);
    await waitFor(() => expect(screen.getByRole("textbox", { name: "Box, line 1" })).toHaveValue("1"));
    await retype(screen.getByRole("textbox", { name: "Box, line 1" }), "2");
    expect(screen.queryByRole("textbox", { name: "Euro pallet, line 2" })).not.toBeInTheDocument();
    expect(screen.getByLabelText("Total, line 1")).toHaveTextContent("1,230 pcs");

    await userEvent.click(screen.getByRole("button", { name: "Post receipt" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/movements\/receipts$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/movements\/receipts$/)[0].body.lines).toEqual([
      {
        variantId: 301,
        entered: {
          packagings: [
            { packagingId: 71, count: 2 },
            { packagingId: 72, count: 2 },
          ],
        },
      },
    ]);

    // The movement reads what was entered.
    await userEvent.click(await screen.findByRole("link", { name: "Open movement 901" }));
    expect(await screen.findByLabelText("Entered as")).toHaveTextContent("2 Euro pallet + 2 Box");
  });

  it("sends quantity alone when no packaging is counted", async () => {
    const fetchMock = stubInventoryApi({ variants: catalog() });
    renderRoute("/inventory/movements/new/receipt?siteId=1&locationId=12");
    await scan(await screen.findByRole("textbox", { name: "Scan an item" }), "PAL-1");
    expect(await screen.findByRole("textbox", { name: "Loose, line 1" })).toHaveValue("1");
    await userEvent.click(screen.getByRole("button", { name: "Post receipt" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/movements\/receipts$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/movements\/receipts$/)[0].body.lines).toEqual([{ variantId: 301, quantity: 1 }]);
  });

  it("takes packagings away in an adjustment's change with Remove", async () => {
    const fetchMock = stubInventoryApi({
      variants: catalog(),
      balances: [{ variantId: 301, locationId: 12, onHand: 1830 }],
    });
    renderRoute("/inventory/movements/new/adjustment?siteId=1&locationId=12");
    await scan(await screen.findByRole("textbox", { name: "Scan an item" }), BOX_GTIN);
    await screen.findByRole("textbox", { name: "Box, line 1" });
    await userEvent.click(screen.getByRole("radio", { name: "Remove" }));
    await choose(document.body, "Reason", "Damaged");
    await userEvent.click(screen.getByRole("button", { name: "Post adjustment" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/movements\/adjustments$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/movements\/adjustments$/)[0].body.lines).toEqual([
      {
        variantId: 301,
        locationId: 12,
        mode: "change",
        entered: { packagings: [{ packagingId: 72, count: 1 }] },
        decrease: true,
      },
    ]);
    expect(fetchMock.state.balances).toEqual([{ variantId: 301, locationId: 12, onHand: 1815 }]);
  });
});

describe("the breakdown (I26)", () => {
  it("shows a location's contents in packagings beside the base quantity", async () => {
    stubInventoryApi({ variants: catalog(), balances: [{ variantId: 301, locationId: 12, onHand: 1830 }] });
    renderRoute("/inventory/locations/12");
    expect(await screen.findByText("3 Euro pallet, 2 Box")).toBeInTheDocument();
    expect(screen.getByText("1,830 pcs")).toBeInTheDocument();
  });

  it("shows it on the product's Stock card", async () => {
    stubInventoryApi({ variants: catalog(), balances: [{ variantId: 301, locationId: 12, onHand: 1837 }] });
    renderRoute("/products/3");
    const oslo = await screen.findByRole("button", { name: /OSL · Oslo lager/ });
    expect(oslo).toHaveTextContent("3 Euro pallet, 2 Box, 7 pcs");
  });
});
