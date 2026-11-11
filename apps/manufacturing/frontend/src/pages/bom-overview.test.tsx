import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { ManufacturingBomOverview, ManufacturingOverviewNode } from "../api/boms";
import type { ManufacturingVariantSearchItem } from "../api/variants";
import { renderRoute } from "../test/route-tree";
import { stubManufacturingApi, variants } from "../test/server";

const [table, board, leg] = variants();

const node = (
  variant: ManufacturingVariantSearchItem,
  patch: Partial<ManufacturingOverviewNode>,
): ManufacturingOverviewNode => ({
  variant: {
    id: variant.id,
    sku: variant.sku,
    productName: variant.productName,
    unit: variant.unit,
    productType: variant.productType,
    productStatus: variant.productStatus,
  },
  bomId: null,
  made: false,
  depth: 1,
  quantityRequired: "1",
  scrapPercent: "0",
  available: null,
  operations: [],
  children: [],
  ...patch,
});

/** A table of four boards and four legs, a leg made by its own BoM of one board; no cost fields at all. */
const overviewWithoutCosts = (): ManufacturingBomOverview => ({
  bomId: 7,
  quantity: "2",
  siteId: null,
  nodeCount: 4,
  root: node(table, {
    bomId: 7,
    made: true,
    depth: 0,
    quantityRequired: "2",
    operations: [
      { operationId: 30, sequence: 10, name: "Assemble", workCenterId: 1, workCenterCode: "SAW", expectedMinutes: 95 },
    ],
    children: [
      node(board, { quantityRequired: "8.4", scrapPercent: "5" }),
      node(leg, {
        bomId: 9,
        made: true,
        quantityRequired: "8",
        children: [node(board, { depth: 2, quantityRequired: "8" })],
      }),
    ],
  }),
});

/** The same, as a caller with costs-view gets it. */
const overviewWithCosts = (): ManufacturingBomOverview => {
  const answer = overviewWithoutCosts();
  answer.root.standardCost = null;
  answer.root.extendedValue = null;
  answer.root.operations[0].cost = "791.666";
  answer.root.children[0].standardCost = "120";
  answer.root.children[0].extendedValue = "1008";
  answer.root.children[1].standardCost = null;
  answer.root.children[1].extendedValue = null;
  answer.root.children[1].children[0].standardCost = null;
  answer.root.children[1].children[0].extendedValue = null;
  return {
    ...answer,
    rolledUpCost: { components: "1008", operations: "791.666", total: "1799.666" },
    costIncomplete: true,
  };
};

describe("the BoM overview", () => {
  it("draws the tree with made and bought badges, required quantities and expected minutes, and no costs without them", async () => {
    const fetchMock = stubManufacturingApi({ meta: { canSeeCosts: false }, overview: overviewWithoutCosts() });
    renderRoute("/manufacturing/boms/7/overview?quantity=2");
    expect(await screen.findByText("TABLE · Oak table")).toBeInTheDocument();
    expect(screen.getByText("Required 8.4 pcs")).toBeInTheDocument();
    expect(screen.getByText("Scrap 5 %")).toBeInTheDocument();
    expect(screen.getByText("Assemble")).toBeInTheDocument();
    expect(screen.getByText("95 min")).toBeInTheDocument();
    expect(screen.getAllByText("Made")).toHaveLength(2);
    expect(screen.getAllByText("Bought")).toHaveLength(1);
    // The leg's own board is one level further down, closed at first.
    expect(screen.getAllByText("BOARD · Oak board")).toHaveLength(1);
    await userEvent.click(screen.getByRole("button", { name: "Expand LEG · Table leg" }));
    expect(screen.getAllByText("BOARD · Oak board")).toHaveLength(2);
    expect(screen.queryByText(/Standard cost/)).not.toBeInTheDocument();
    expect(screen.queryByText("Rolled-up cost")).not.toBeInTheDocument();
    expect(screen.queryByText("The cost is incomplete")).not.toBeInTheDocument();
    expect(fetchMock.actualCalls.map(([url]) => String(url))).toContain(
      "/api/v1/manufacturing/boms/7/overview?quantity=2",
    );
  });

  it("shows the standard costs, the rolled-up cost and the incomplete flag when the server answers them", async () => {
    stubManufacturingApi({ overview: overviewWithCosts() });
    renderRoute("/manufacturing/boms/7/overview");
    expect(await screen.findByText("Rolled-up cost")).toBeInTheDocument();
    expect(screen.getByText("1,799.67 NOK")).toBeInTheDocument();
    expect(screen.getByText("Standard cost 120.00 NOK")).toBeInTheDocument();
    expect(screen.getByText("Value 1,008.00 NOK")).toBeInTheDocument();
    // The one operation's cost, and the operations' part of the rolled-up cost.
    expect(screen.getAllByText("791.67 NOK")).toHaveLength(2);
    expect(screen.getByText("The cost is incomplete")).toBeInTheDocument();
  });

  it("asks again at the quantity typed and the site chosen", async () => {
    const fetchMock = stubManufacturingApi({ meta: { canSeeCosts: false }, overview: overviewWithoutCosts() });
    const { router } = renderRoute("/manufacturing/boms/7/overview");
    await screen.findByText("TABLE · Oak table");
    const field = screen.getByRole("textbox", { name: "Quantity to make" });
    await userEvent.type(field, "2,5{Enter}");
    await waitFor(() => expect(router.state.location.search).toEqual({ quantity: "2.5" }));
    await waitFor(() =>
      expect(fetchMock.actualCalls.map(([url]) => String(url))).toContain(
        "/api/v1/manufacturing/boms/7/overview?quantity=2.5",
      ),
    );
    await userEvent.clear(field);
    await userEvent.type(field, "0{Enter}");
    expect(await screen.findByText("More than 0, with at most three decimals.")).toBeInTheDocument();
  });
});
