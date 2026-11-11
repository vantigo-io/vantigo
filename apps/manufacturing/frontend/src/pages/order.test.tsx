import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { jsonResponse } from "../test/api";
import { sent } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import { manufacturingProblem, orderFixture, stubManufacturingApi } from "../test/server";

const PRODUCTION = { id: 70, orderId: 5, orderNumber: "MO-12", quantity: "10", outputScrap: "0", lines: [] };

describe("the production dialog", () => {
  it("prefills the consumption in proportion and reads it again when the quantity changes", async () => {
    const fetchMock = stubManufacturingApi({ meta: { canWorkShopFloor: true } });
    renderRoute("/manufacturing/orders/5");
    await userEvent.click(await screen.findByRole("button", { name: "Register production" }));
    const dialog = await screen.findByRole("dialog");
    // What remains, 10 tables, takes the whole 40 boards.
    expect(await within(dialog).findByDisplayValue("40.000")).toBeInTheDocument();
    expect(within(dialog).getByLabelText(/Good quantity produced/)).toHaveValue("10");

    const quantity = within(dialog).getByLabelText(/Good quantity produced/);
    await userEvent.clear(quantity);
    await userEvent.type(quantity, "4");
    expect(await within(dialog).findByDisplayValue("16.000")).toBeInTheDocument();
    const drafts = fetchMock.actualCalls.map(([url]) => String(url)).filter((url) => url.includes("production-draft"));
    expect(drafts.at(-1)).toContain("quantity=4");
  });

  it("warns of a deviating consumption and posts anyway with the same request id", async () => {
    let posts = 0;
    const fetchMock = stubManufacturingApi({
      meta: { canWorkShopFloor: true },
      write: (method, path) => {
        if (method !== "POST" || path !== "/orders/5/productions") return undefined;
        posts += 1;
        return posts === 1
          ? manufacturingProblem(409, "consumption_differs", {
              deviations: [{ componentId: 60, sku: "BOARD", expected: "40", consumed: "55" }],
            })
          : jsonResponse(201, PRODUCTION);
      },
    });
    renderRoute("/manufacturing/orders/5");
    await userEvent.click(await screen.findByRole("button", { name: "Register production" }));
    const dialog = await screen.findByRole("dialog");
    const consumed = await within(dialog).findByLabelText("Consumed of BOARD");
    await waitFor(() => expect(consumed).toHaveValue("40.000"));
    await userEvent.clear(consumed);
    await userEvent.type(consumed, "55");
    await userEvent.click(within(dialog).getByRole("button", { name: "Post" }));

    expect(await within(dialog).findByText("The consumption differs from the bill of materials")).toBeInTheDocument();
    expect(within(dialog).getByText("BOARD: 55 pcs consumed, 40 pcs expected")).toBeInTheDocument();
    await userEvent.click(within(dialog).getByRole("button", { name: "Post anyway" }));

    await waitFor(() => expect(sent(fetchMock, "POST", /\/orders\/5\/productions$/)).toHaveLength(2));
    const [first, second] = sent(fetchMock, "POST", /\/orders\/5\/productions$/).map((call) => call.body);
    expect(first).toMatchObject({ quantity: "10", lines: [{ componentId: 60, consumed: "55" }] });
    expect(first).not.toHaveProperty("acceptDeviation");
    expect(typeof first.clientRequestId).toBe("string");
    expect(second).toEqual({ ...first, acceptDeviation: true });
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  });
});

describe("the close dialog", () => {
  const short = orderFixture({ status: "in_progress", produced: "6" });

  it.each([
    ["Yes, create a backorder for the rest", true],
    ["No, close it short", false],
  ])("asks whether to create a backorder when less was produced: %s", async (answer, backorder) => {
    const fetchMock = stubManufacturingApi({
      meta: { canManageOrders: true },
      orders: [short],
      write: (method, path) =>
        method === "POST" && path === "/orders/5/close"
          ? jsonResponse(200, { order: { ...short, status: "done" }, backorder: null, shortLines: [] })
          : undefined,
    });
    renderRoute("/manufacturing/orders/5");
    await userEvent.click(await screen.findByRole("button", { name: "Close" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText("Create backorder for the rest?")).toBeInTheDocument();
    const close = within(dialog).getByRole("button", { name: "Close" });
    expect(close).toBeDisabled();
    await userEvent.click(within(dialog).getByLabelText(answer));
    await userEvent.click(close);
    await waitFor(() => expect(sent(fetchMock, "POST", /\/orders\/5\/close$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/orders\/5\/close$/)[0].body).toEqual({ revision: 4, backorder });
  });

  it("asks nothing and sends only the revision when the quantity was produced", async () => {
    const done = orderFixture({ status: "in_progress", produced: "10" });
    const fetchMock = stubManufacturingApi({
      meta: { canManageOrders: true },
      orders: [done],
      write: (method, path) =>
        method === "POST" && path === "/orders/5/close"
          ? jsonResponse(200, { order: { ...done, status: "done" }, backorder: null, shortLines: [] })
          : undefined,
    });
    renderRoute("/manufacturing/orders/5");
    await userEvent.click(await screen.findByRole("button", { name: "Close" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).queryByText("Create backorder for the rest?")).not.toBeInTheDocument();
    await userEvent.click(within(dialog).getByRole("button", { name: "Close" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/orders\/5\/close$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/orders\/5\/close$/)[0].body).toEqual({ revision: 4 });
  });
});

describe("the order page", () => {
  it("names what is missing per line and offers Reserve now for a short order", async () => {
    const order = orderFixture({
      reservationStatus: "partly_reserved",
      availability: "partly_available",
      components: [
        {
          ...orderFixture().components[0],
          reserved: "30",
          available: "0",
          missing: "10",
          covered: false,
        },
      ],
    });
    stubManufacturingApi({ meta: { canManageOrders: true }, orders: [order] });
    renderRoute("/manufacturing/orders/5");
    expect(await screen.findByText("10 pcs missing")).toBeInTheDocument();
    expect(screen.getByText("Not everything is reserved")).toBeInTheDocument();
    expect(screen.getAllByRole("button", { name: "Reserve now" }).length).toBeGreaterThan(0);
  });

  it("offers a draft Confirm and Cancel but no production", async () => {
    stubManufacturingApi({
      meta: { canManageOrders: true },
      orders: [orderFixture({ status: "draft", reservationStatus: null })],
    });
    renderRoute("/manufacturing/orders/5");
    expect(await screen.findByRole("button", { name: "Confirm" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Cancel order" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Register production" })).not.toBeInTheDocument();
  });
});
