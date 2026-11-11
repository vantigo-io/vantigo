import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { sent } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import { manufacturingProblem, stubManufacturingApi } from "../test/server";

describe("the BoM editor", () => {
  it("saves the BoM whole under its revision, operations by key and id, lines by operation key", async () => {
    const fetchMock = stubManufacturingApi();
    renderRoute("/manufacturing/boms/7");
    await screen.findByDisplayValue("Cut");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(sent(fetchMock, "PUT", /\/boms\/7$/)).toHaveLength(1));
    const [{ body }] = sent(fetchMock, "PUT", /\/boms\/7$/);
    expect(body).toMatchObject({
      revision: 3,
      code: "Rev B",
      outputQuantity: "1",
      operations: [{ key: "30", id: 30, sequence: 10, name: "Cut", workCenterId: 1, minutesPerCycle: "4" }],
      lines: [{ variantId: 2, quantity: "4", scrapPercent: "0", operationKey: "30", sortOrder: 10 }],
    });
    expect(body).not.toHaveProperty("variantId");
  });

  it("clears a removed operation from the lines that named it", async () => {
    const fetchMock = stubManufacturingApi();
    renderRoute("/manufacturing/boms/7");
    await screen.findByDisplayValue("Cut");
    await userEvent.click(screen.getByRole("button", { name: "Remove operation 1" }));
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(sent(fetchMock, "PUT", /\/boms\/7$/)).toHaveLength(1));
    const [{ body }] = sent(fetchMock, "PUT", /\/boms\/7$/);
    expect(body.operations).toEqual([]);
    expect(body.lines).toEqual([expect.objectContaining({ operationKey: null })]);
  });

  it("marks a refused line on its row and names a cycle by its SKUs", async () => {
    stubManufacturingApi({
      write: (method) =>
        method === "PUT"
          ? manufacturingProblem(400, "bom_cycle", {
              detail: "cycle",
              lines: [{ index: 0, code: "variant_not_goods", detail: "Not goods" }],
              path: [
                { variantId: 1, sku: "TABLE" },
                { variantId: 2, sku: "BOARD" },
                { variantId: 1, sku: "TABLE" },
              ],
            })
          : undefined,
    });
    renderRoute("/manufacturing/boms/7");
    await screen.findByDisplayValue("Cut");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(
      await screen.findByText("A component is made of the product itself: TABLE → BOARD → TABLE."),
    ).toBeInTheDocument();
    expect(screen.getByText("Only goods can be made or used as components, not services.")).toBeInTheDocument();
  });

  it("puts a server's field errors on their fields", async () => {
    stubManufacturingApi({
      write: (method) =>
        method === "PUT"
          ? manufacturingProblem(400, "quantity_invalid", { errors: { "lines[0].quantity": ["Too many decimals"] } })
          : undefined,
    });
    renderRoute("/manufacturing/boms/7");
    await screen.findByDisplayValue("Cut");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(await screen.findByText("Too many decimals")).toBeInTheDocument();
  });

  it("is read only without boms-manage", async () => {
    stubManufacturingApi({ meta: { canManageBoms: false } });
    renderRoute("/manufacturing/boms/7");
    expect(await screen.findByDisplayValue("Cut")).toBeDisabled();
    expect(screen.queryByRole("button", { name: "Save" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Archive" })).not.toBeInTheDocument();
  });
});
