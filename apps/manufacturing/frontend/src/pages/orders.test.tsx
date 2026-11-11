import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { ordersQueryString } from "../api/orders";
import { orderFilterOf, validateOrdersSearch } from "../lib/search";
import { renderRoute } from "../test/route-tree";
import { orderFixture, stubManufacturingApi } from "../test/server";

const listCalls = (fetchMock: ReturnType<typeof stubManufacturingApi>) =>
  fetchMock.actualCalls
    .map(([url]) => new URL(String(url), "http://localhost"))
    .filter((url) => url.pathname === "/api/v1/manufacturing/orders");

describe("the order list's query", () => {
  it("sends only the filters set, each status repeated", () => {
    expect(ordersQueryString({})).toBe("");
    expect(
      ordersQueryString(
        orderFilterOf(
          validateOrdersSearch({
            status: ["confirmed", "in_progress", "nonsense"],
            siteId: 2,
            variantId: "3",
            dueFrom: "2026-11-01",
            dueTo: "2026-11-30",
            late: true,
            origin: "manual",
            responsibleUserId: "7d1f2a51-9b0c-4c1e-8a4e-2f3b6c7d8e90",
            reservation: "partly_reserved",
            q: " MO-12 ",
            sort: "priority",
            page: 2,
          }),
        ),
      ),
    ).toBe(
      "?status=confirmed&status=in_progress&siteId=2&variantId=3&dueFrom=2026-11-01&dueTo=2026-11-30&late=true" +
        "&origin=manual&responsibleUserId=7d1f2a51-9b0c-4c1e-8a4e-2f3b6c7d8e90&reservation=partly_reserved" +
        "&q=MO-12&sort=priority&page=2",
    );
  });

  it("drops what is not a filter the contract knows", () => {
    expect(
      validateOrdersSearch({ status: "open", late: "no", sort: "name", dueFrom: "11/01", origin: "x", page: 1 }),
    ).toEqual({});
    expect(validateOrdersSearch({ status: "draft" })).toEqual({ status: ["draft"] });
  });
});

describe("the order list", () => {
  it("shows the open orders with their reservation status, not their availability", async () => {
    const fetchMock = stubManufacturingApi({
      orders: [orderFixture({ reservationStatus: "partly_reserved", availability: "waiting", late: true })],
    });
    renderRoute("/manufacturing/orders");
    expect(await screen.findByRole("link", { name: "MO-12" })).toHaveAttribute("href", "/manufacturing/orders/5");
    const table = within(screen.getByRole("table"));
    expect(table.getByText("Partly reserved")).toBeInTheDocument();
    expect(screen.queryByText("Waiting")).not.toBeInTheDocument();
    expect(table.getByText("Late")).toBeInTheDocument();
    expect(listCalls(fetchMock)[0].search).toBe("");
    expect(screen.queryByRole("link", { name: "New manufacturing order" })).not.toBeInTheDocument();
  });

  it("marks an order whose sales order was cancelled", async () => {
    stubManufacturingApi({
      orders: [
        orderFixture({
          origin: { kind: "sales_order", orderId: 77, lineId: 501, reference: "SO-1042", cancelled: true },
        }),
      ],
    });
    renderRoute("/manufacturing/orders");
    await screen.findByRole("link", { name: "MO-12" });
    expect(within(screen.getByRole("table")).getByTestId("origin-cancelled")).toHaveTextContent(
      "Sales order cancelled",
    );
  });

  it("asks the server again with the filter chosen", async () => {
    const fetchMock = stubManufacturingApi({ meta: { canManageOrders: true } });
    renderRoute("/manufacturing/orders?siteId=1");
    expect(await screen.findByRole("link", { name: "New manufacturing order" })).toBeInTheDocument();
    await waitFor(() => expect(listCalls(fetchMock).at(-1)?.searchParams.get("siteId")).toBe("1"));
    await userEvent.click(screen.getByRole("switch", { name: "Late only" }));
    await waitFor(() => expect(listCalls(fetchMock).at(-1)?.searchParams.get("late")).toBe("true"));
    expect(listCalls(fetchMock).at(-1)?.searchParams.get("siteId")).toBe("1");
  });
});
