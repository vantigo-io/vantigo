import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { droppedMember } from "../lib/schedule";
import { sent } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import { bomFixture, stubManufacturingApi, workOrderFixture } from "../test/server";

// The routing upgrades on the screens (manufacturing phase 2 M112–M119).

describe("Group work on a member's queue", () => {
  it("lists a group's work apart from the queue and starts it at this work center", async () => {
    const workOrder = workOrderFixture({ id: 77, workCenterGroup: { id: 5, code: "SAWS", name: "Saws" } });
    const fetchMock = stubManufacturingApi({
      meta: { canWorkShopFloor: true },
      workOrders: [workOrder],
      groupWork: { 2: [workOrder] },
    });
    renderRoute("/manufacturing/shop-floor/work-centers/2");
    expect(await screen.findByText("Group work")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Start here" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/work-orders\/77\/start$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/work-orders\/77\/start$/)[0].body).toEqual({ workCenterId: 2 });
  });
});

describe("Register quantity", () => {
  it("passes good units on from a work order that is not the last, once per request id", async () => {
    const workOrder = workOrderFixture({
      id: 41,
      status: "in_progress",
      last: false,
      sendAheadQuantity: "10",
      quantityDone: "4",
    });
    const fetchMock = stubManufacturingApi({ meta: { canWorkShopFloor: true }, workOrders: [workOrder] });
    renderRoute("/manufacturing/shop-floor/work-orders/41");
    expect(await screen.findByText("4 pcs of 10 pcs passed on before the next may start")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Register quantity" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.type(within(dialog).getByLabelText("Quantity"), "6");
    await userEvent.click(within(dialog).getByRole("button", { name: "Register quantity" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/work-orders\/41\/quantity$/)).toHaveLength(1));
    const { body } = sent(fetchMock, "POST", /\/work-orders\/41\/quantity$/)[0];
    expect(body.quantity).toBe("6");
    expect(body.clientRequestId).toMatch(/^[0-9a-f-]{36}$/);
  });
});

describe("the BoM editor's graph", () => {
  it("shows the operations in steps by what each depends on", async () => {
    const bom = bomFixture();
    const [cut] = bom.operations;
    bom.operations = [
      cut,
      { ...cut, id: 31, key: "31", sequence: 20, name: "Paint", dependsOn: ["30"] },
      { ...cut, id: 32, key: "32", sequence: 30, name: "Drill", dependsOn: ["30"] },
    ];
    stubManufacturingApi({ boms: [bom] });
    renderRoute(`/manufacturing/boms/${bom.id}`);
    expect(await screen.findByText("Order of the operations")).toBeInTheDocument();
    expect(screen.getByText("Step 1: Cut")).toBeInTheDocument();
    expect(screen.getByText("Step 2: Paint · Drill")).toBeInTheDocument();
  });
});

describe("a drop on the board", () => {
  it("moves a group work order only to another member's row", () => {
    expect(droppedMember({ memberIds: [2, 3] }, 1, 3)).toBe(3);
    expect(droppedMember({ memberIds: [2, 3] }, 1, 4)).toBeUndefined();
    expect(droppedMember({ memberIds: [2] }, 1, 1)).toBeUndefined();
    expect(droppedMember({ memberIds: [] }, 1, 2)).toBeUndefined();
  });
});
