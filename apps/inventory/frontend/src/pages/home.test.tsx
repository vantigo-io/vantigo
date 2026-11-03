import { screen } from "@testing-library/react";
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
