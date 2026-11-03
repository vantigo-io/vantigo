import { screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { renderRoute } from "../test/route-tree";
import { stubInventoryApi } from "../test/server";

describe("the home page's postings", () => {
  it("offers the three postings to a caller who may view locations and stock and write them", async () => {
    stubInventoryApi();
    renderRoute("/inventory");
    expect(await screen.findByRole("link", { name: "New receipt" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "New adjustment" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "New move" })).toBeInTheDocument();
  });

  it("offers no receipt to a caller with Receive goods but not the stock view (design §5)", async () => {
    stubInventoryApi({ meta: { canViewStock: false, canAdjust: false, canReceive: true } });
    renderRoute("/inventory");
    expect(await screen.findByRole("link", { name: "OSL · Oslo lager" })).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "New receipt" })).not.toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "New adjustment" })).not.toBeInTheDocument();
  });

  it("and the receipt's page says it is not allowed instead of showing the form", async () => {
    stubInventoryApi({ meta: { canViewStock: false, canAdjust: false, canReceive: true } });
    renderRoute("/inventory/movements/new/receipt");
    expect(await screen.findByText(/You cannot receive goods/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Post receipt" })).not.toBeInTheDocument();
  });
});

describe("the site cards", () => {
  it("read the locations, stock lines and last receipt off the sites, with no request per card", async () => {
    const fetchMock = stubInventoryApi({
      balances: [
        { variantId: 101, locationId: 12, onHand: 2 },
        { variantId: 201, locationId: 13, onHand: 1 },
      ],
      movements: [
        {
          id: 77,
          kind: "receipt",
          clientRequestId: "k",
          site: { id: 1, code: "OSL", name: "Oslo lager", kind: "warehouse", status: "active" },
          postedAt: "2026-11-01T10:00:00Z",
          postedBy: { displayName: "Kari" },
          lines: [],
        },
      ],
    });
    renderRoute("/inventory");
    const oslo = (await screen.findByRole("link", { name: "OSL · Oslo lager" })).closest(
      ".mantine-Card-root",
    ) as HTMLElement;
    expect(within(oslo).getByText("3 locations")).toBeInTheDocument();
    expect(within(oslo).getByText("2 stock lines")).toBeInTheDocument();
    expect(within(oslo).getByText(/^Last receipt /)).toBeInTheDocument();
    const bergen = screen
      .getByRole("link", { name: "BGO · Bergen butikk" })
      .closest(".mantine-Card-root") as HTMLElement;
    expect(within(bergen).getByText("1 location")).toBeInTheDocument();
    expect(within(bergen).getByText("No receipts yet")).toBeInTheDocument();
    const asked = fetchMock.actualCalls.map(([url]) => String(url));
    expect(asked.filter((url) => /\/locations|\/movements|\/contents/.test(url))).toEqual([]);
  });

  it("show no stock figures to a caller without stock-view", async () => {
    stubInventoryApi({ meta: { canViewStock: false } });
    renderRoute("/inventory");
    const oslo = (await screen.findByRole("link", { name: "OSL · Oslo lager" })).closest(
      ".mantine-Card-root",
    ) as HTMLElement;
    expect(within(oslo).getByText("3 locations")).toBeInTheDocument();
    expect(within(oslo).queryByText(/stock line/)).not.toBeInTheDocument();
  });
});
