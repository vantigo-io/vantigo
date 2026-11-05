import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { renderRoute } from "../test/route-tree";
import { stubInventoryApi } from "../test/server";

describe("the product's Stock card", () => {
  it("says there is no stock and offers Receive and Adjust as the caller may", async () => {
    stubInventoryApi({ meta: { canAdjust: false } });
    renderRoute("/products/2");
    expect(await screen.findByText("No stock")).toBeInTheDocument();
    const receive = await screen.findByRole("link", { name: "Receive" });
    expect(receive).toHaveAttribute("href", "/inventory/movements/new/receipt?variantId=201");
    expect(screen.queryByRole("link", { name: "Adjust" })).not.toBeInTheDocument();
  });

  it("totals the stock in several sites, each expanding to its locations by full code", async () => {
    stubInventoryApi({
      balances: [
        { variantId: 201, locationId: 13, onHand: 2 },
        { variantId: 201, locationId: 12, onHand: 3 },
        { variantId: 201, locationId: 21, onHand: 4.5 },
      ],
    });
    renderRoute("/products/2");
    expect(await screen.findByLabelText("Total on hand")).toHaveTextContent("9.5 pcs");
    const oslo = screen.getByRole("button", { name: /OSL · Oslo lager/ });
    expect(oslo).toHaveTextContent("5 pcs");
    expect(screen.getByRole("button", { name: /BGO · Bergen butikk/ })).toHaveTextContent("4.5 pcs");
    await userEvent.click(oslo);
    const codes = await screen.findAllByText(/^OSL-A-0\d$/);
    expect(codes.map((code) => code.textContent)).toEqual(["OSL-A-01", "OSL-A-02"]);
    expect(screen.getByRole("link", { name: "Stock and movements of BOLT-10" })).toHaveAttribute(
      "href",
      "/inventory/variants/201",
    );
  });

  it("shows each variant of a product with several, with a link to each one's stock", async () => {
    stubInventoryApi({
      balances: [
        { variantId: 101, locationId: 12, onHand: 2 },
        { variantId: 102, locationId: 13, onHand: 4 },
      ],
    });
    renderRoute("/products/1");
    expect(await screen.findByLabelText("Total on hand")).toHaveTextContent("6 pcs");
    expect(screen.getByRole("link", { name: "Stock and movements of TS-M" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Stock and movements of TS-L" })).toBeInTheDocument();
    expect(screen.getByText("TS-L")).toBeInTheDocument();
  });

  it("shows what orders reserved and what is available per site, warning when available is below zero", async () => {
    stubInventoryApi({
      balances: [
        { variantId: 201, locationId: 12, onHand: 3 },
        { variantId: 201, locationId: 21, onHand: 4 },
      ],
      reservations: [
        { variantId: 201, siteId: 1, quantity: 5 },
        { variantId: 201, siteId: 2, quantity: 1 },
      ],
    });
    renderRoute("/products/2");
    const oslo = await screen.findByRole("button", { name: /OSL · Oslo lager/ });
    expect(oslo).toHaveTextContent("Reserved 5 pcs · available -2 pcs");
    expect(screen.getByRole("button", { name: /BGO · Bergen butikk/ })).toHaveTextContent(
      "Reserved 1 pcs · available 3 pcs",
    );
    expect(screen.getByRole("alert")).toHaveTextContent("More is reserved than lies here");
  });

  it("lists a site holding only a reservation", async () => {
    stubInventoryApi({ reservations: [{ variantId: 201, siteId: 1, quantity: 2 }] });
    renderRoute("/products/2");
    expect(await screen.findByRole("button", { name: /OSL · Oslo lager/ })).toHaveTextContent(
      "Reserved 2 pcs · available -2 pcs",
    );
    expect(screen.queryByText("No stock")).not.toBeInTheDocument();
  });

  it("is not drawn for a product that answers no variants (a Service)", async () => {
    const fetchMock = stubInventoryApi({ productStock: { 3: { productId: 3, variants: [] } } });
    renderRoute("/products/3");
    await waitFor(() =>
      expect(fetchMock.actualCalls.some(([url]) => String(url).endsWith("/products/3/stock"))).toBe(true),
    );
    await waitFor(() => expect(screen.queryByText("Stock")).not.toBeInTheDocument());
    expect(screen.queryByTestId("content-skeleton")).not.toBeInTheDocument();
  });
});
