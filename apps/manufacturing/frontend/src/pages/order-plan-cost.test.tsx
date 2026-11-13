import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { sent } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import { orderFixture, stubManufacturingApi, workOrderFixture } from "../test/server";

const parts = (total: string, incomplete = false) => ({
  components: "1000.00",
  machine: "200.00",
  labour: "300.00",
  total,
  incomplete,
});

const cost = {
  byProducts: [],
  mainActual: "1650.00",
  estimate: parts("1500.00"),
  actual: parts("1650.00", true),
  actualFixed: true,
  deviation: {
    components: { amount: "0.00", percent: "0.0" },
    machine: { amount: "0.00", percent: "0.0" },
    labour: { amount: "0.00", percent: "0.0" },
    total: { amount: "150.00", percent: "10.0" },
  },
  costPerUnit: "165.00",
  downtime: [{ reason: "breakdown" as const, minutes: 45 }],
};

describe("the order's plan", () => {
  it("offers Plan and Unplan to whoever manages orders, each under the order's revision", async () => {
    const planned = orderFixture({ plannedStart: "2026-11-12T07:00:00Z", plannedFinish: "2026-11-12T09:00:00Z" });
    const fetchMock = stubManufacturingApi({ meta: { canManageOrders: true }, orders: [planned] });
    renderRoute("/manufacturing/orders/5");
    await userEvent.click(await screen.findByRole("button", { name: "Plan again" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/orders\/5\/plan$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/orders\/5\/plan$/)[0].body).toEqual({ revision: 4 });
    await userEvent.click(screen.getByRole("button", { name: "Unplan" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/orders\/5\/unplan$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/orders\/5\/unplan$/)[0].body).toEqual({ revision: 4 });
  });

  it("shows the planned window in the edit form, read-only, and never sends it (M34)", async () => {
    const planned = orderFixture({ plannedStart: "2026-11-12T07:00:00Z", plannedFinish: "2026-11-12T09:00:00Z" });
    const fetchMock = stubManufacturingApi({ meta: { canManageOrders: true }, orders: [planned] });
    renderRoute("/manufacturing/orders/5");
    await userEvent.click(await screen.findByRole("button", { name: "Edit" }));
    const dialog = await screen.findByRole("dialog");
    const window = within(dialog).getByLabelText("Planned");
    expect(window).toHaveAttribute("readonly");
    expect(within(dialog).queryByLabelText("Planned start")).not.toBeInTheDocument();
    expect(within(dialog).queryByLabelText("Planned finish")).not.toBeInTheDocument();
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(sent(fetchMock, "PUT", /\/orders\/5$/)).toHaveLength(1));
    const body = sent(fetchMock, "PUT", /\/orders\/5$/)[0].body as Record<string, unknown>;
    expect(body).not.toHaveProperty("plannedStart");
    expect(body).not.toHaveProperty("plannedFinish");
  });

  it("offers neither without orders-manage", async () => {
    stubManufacturingApi({ meta: { canManageOrders: false } });
    renderRoute("/manufacturing/orders/5");
    await screen.findByText("MO-12 · TABLE", { exact: false });
    expect(screen.queryByRole("button", { name: "Plan" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Unplan" })).not.toBeInTheDocument();
  });

  it("does not offer Plan on a draft", async () => {
    stubManufacturingApi({ meta: { canManageOrders: true }, orders: [orderFixture({ status: "draft" })] });
    renderRoute("/manufacturing/orders/5");
    await screen.findByRole("button", { name: "Confirm" });
    expect(screen.queryByRole("button", { name: "Plan" })).not.toBeInTheDocument();
  });

  it("flags an order planned to finish after its due date and shows the default requested start", async () => {
    stubManufacturingApi({ orders: [orderFixture({ lateByDays: 3 })] });
    renderRoute("/manufacturing/orders/5");
    expect(await screen.findByText("Late by 3 days")).toBeInTheDocument();
    expect(screen.getByText(/\(default\)/)).toBeInTheDocument();
  });
});

describe("the order's cost", () => {
  it("is not shown when the server left cost out, whatever the meta says", async () => {
    stubManufacturingApi({ meta: { canSeeCosts: true } });
    renderRoute("/manufacturing/orders/5");
    await screen.findByText("MO-12 · TABLE", { exact: false });
    expect(screen.queryByRole("heading", { name: "Cost" })).not.toBeInTheDocument();
  });

  it("shows the estimate, the actual, the deviation per part, the cost per unit, the incomplete note and the downtime", async () => {
    stubManufacturingApi({ orders: [orderFixture({ status: "done", cost })] });
    renderRoute("/manufacturing/orders/5");
    const section = (await screen.findByRole("heading", { name: "Cost" })).closest("section") as HTMLElement;
    const total = within(section).getByText("Total").closest("tr") as HTMLElement;
    expect(within(total).getByText("1,500.00 NOK")).toBeInTheDocument();
    expect(within(total).getByText("1,650.00 NOK")).toBeInTheDocument();
    expect(within(total).getByText("150.00 NOK")).toBeInTheDocument();
    expect(within(total).getByText("10.0 %")).toBeInTheDocument();
    expect(within(section).getByText("165.00 NOK")).toBeInTheDocument();
    expect(within(section).getByText("Fixed at close")).toBeInTheDocument();
    expect(within(section).getByText(/The actual cost is incomplete/)).toBeInTheDocument();
    expect(within(section).queryByText(/The estimate is incomplete/)).not.toBeInTheDocument();
    expect(within(section).getByText("Breakdown: 45 min")).toBeInTheDocument();
  });
});

describe("the work orders' duration deviation", () => {
  it("shows each work order's actual time and its deviation once time has run", async () => {
    stubManufacturingApi({
      workOrders: [
        workOrderFixture({ actualMinutes: 52, deviationMinutes: 12, deviationPercent: "30.0", status: "done" }),
        workOrderFixture({ id: 41, sequence: 20, name: "Paint" }),
      ],
    });
    renderRoute("/manufacturing/orders/5");
    await userEvent.click(await screen.findByRole("tab", { name: "Work orders" }));
    const cut = (await screen.findByRole("link", { name: "Cut" })).closest("tr") as HTMLElement;
    expect(within(cut).getByText("52 min")).toBeInTheDocument();
    expect(within(cut).getByText("+12 min (+30.0 %)")).toBeInTheDocument();
    const paint = screen.getByRole("link", { name: "Paint" }).closest("tr") as HTMLElement;
    expect(within(paint).getAllByText("—").length).toBeGreaterThan(0);
  });
});
