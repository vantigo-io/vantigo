import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { sent } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import { stubInventoryApi } from "../test/server";

const balances = [
  { variantId: 201, locationId: 11, onHand: 3 },
  { variantId: 101, locationId: 12, onHand: 2 },
];

describe("the location page", () => {
  it("shows what lies below by default, and the location's own with This location only", async () => {
    const fetchMock = stubInventoryApi({ balances });
    renderRoute("/inventory/locations/11");
    expect(await screen.findByRole("heading", { name: /OSL-A/ })).toBeInTheDocument();
    const contents = (await screen.findByRole("heading", { name: "Contents" })).closest("div") as HTMLElement;
    await waitFor(() => expect(within(contents.parentElement as HTMLElement).getAllByRole("row")).toHaveLength(3));
    expect(screen.getByRole("link", { name: "TS-M" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "OSL-A-01" })).toBeInTheDocument();

    await userEvent.click(screen.getByRole("switch", { name: "This location only" }));
    await waitFor(() => expect(screen.queryByRole("link", { name: "TS-M" })).not.toBeInTheDocument());
    expect(screen.getByRole("link", { name: "BOLT-10" })).toBeInTheDocument();
    expect(sent(fetchMock, "GET", /\/locations\/11\/contents\?subtree=false$/)).toHaveLength(1);
  });

  it("offers Adjust, Move from here and Receive here, prefilled with the location", async () => {
    stubInventoryApi({ balances });
    renderRoute("/inventory/locations/12");
    expect(await screen.findByRole("link", { name: "Receive here" })).toHaveAttribute(
      "href",
      "/inventory/movements/new/receipt?siteId=1&locationId=12",
    );
    expect(screen.getByRole("link", { name: "Move from here" })).toHaveAttribute(
      "href",
      "/inventory/movements/new/move?siteId=1&locationId=12",
    );
    expect(screen.getByRole("link", { name: "Adjust" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Actions for OSL-A-01" })).toBeInTheDocument();
  });

  it("links All movements to the movements of its site, filtered to the location", async () => {
    stubInventoryApi({ balances });
    renderRoute("/inventory/locations/12");
    expect(await screen.findByRole("link", { name: "All movements" })).toHaveAttribute(
      "href",
      "/inventory/movements?siteId=1&locationId=12",
    );
  });

  it("offers no posting to a caller who may write stock but not view it (design §5)", async () => {
    stubInventoryApi({ balances, meta: { canViewStock: false } });
    renderRoute("/inventory/locations/12");
    expect(await screen.findByRole("heading", { name: /OSL-A-01/ })).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "Receive here" })).not.toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "Move from here" })).not.toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "Adjust" })).not.toBeInTheDocument();
  });

  it("hides what the caller may not do", async () => {
    stubInventoryApi({ meta: { canAdjust: false, canReceive: false, canManageLocations: false, canViewStock: false } });
    renderRoute("/inventory/locations/12");
    expect(await screen.findByRole("heading", { name: /OSL-A-01/ })).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "Receive here" })).not.toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "Adjust" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Actions for OSL-A-01" })).not.toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Contents" })).not.toBeInTheDocument();
  });
});
