import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { setLanguagePreference } from "@vantigo/frontend-shell";
import { describe, expect, it } from "vitest";
import type { DueOrder, DueOrdersView, FromOrdersResult } from "../api/from-orders";
import { jsonResponse, path, refusal, sent } from "../test/api";
import { stubFetch } from "../test/fetch";
import { renderAtHost } from "../test/route-tree";
import { FromOrdersPage } from "./from-orders";

const DUE = "/api/v1/invoices/from-orders/due?customerId=2001";
const POST = "/api/v1/invoices/from-orders";

const order = (overrides: Partial<DueOrder> = {}): DueOrder => ({
  id: 5210,
  number: 2210,
  customerId: 2001,
  customerName: "Acme AS",
  currency: "NOK",
  yourReference: "PO-1",
  deliveryDate: "2026-09-01",
  paymentTermsDays: 30,
  lines: [
    {
      lineId: 1,
      position: 1,
      description: "Hylle",
      unit: "stk",
      toInvoice: 5,
      unitPrice: 1200,
      discountPercent: 10,
      vatCodeId: 1,
      invoicePolicy: "order",
      amount: 5400,
    },
  ],
  ...overrides,
});

const dueView = (): DueOrdersView => ({
  orders: [
    order(),
    order({
      id: 5211,
      number: 2211,
      lines: [
        {
          lineId: 3,
          position: 1,
          description: "Bok",
          unit: "stk",
          toInvoice: 2,
          unitPrice: 399,
          discountPercent: 0,
          vatCodeId: 2,
          invoicePolicy: "order",
          amount: 798,
        },
      ],
    }),
    order({ id: 5212, number: 2212, yourReference: "PO-2", blocked: "on_hold" }),
  ],
  warnings: [],
});

const server = (answer: () => Response = () => jsonResponse(201, made([1001]))) =>
  stubFetch((input: RequestInfo | URL, init?: RequestInit) => {
    const url = path(input);
    const method = init?.method ?? "GET";
    if (url === DUE) return jsonResponse(200, dueView());
    if (url === POST && method === "POST") return answer();
    return new Response(null, { status: 404 });
  });

const made = (ids: number[]): FromOrdersResult => ({
  invoices: ids.map((id, i) => ({
    invoiceId: id,
    customerId: 2001,
    orderIds: [5210 + i],
    orderNumbers: [2210 + i],
    separatedBy: [],
  })),
});

const open = async () => {
  const { router } = renderAtHost(<FromOrdersPage canViewCustomers={false} initialCustomerId={2001} />);
  await screen.findByRole("checkbox", { name: "Choose order 2210" });
  return { router };
};

const next = () => userEvent.click(screen.getByRole("button", { name: "Next" }));

describe("the From orders wizard", () => {
  it("lists the customer's orders, a held one with why, and the lines left to invoice with what remains", async () => {
    server();
    await open();

    expect(screen.getByRole("checkbox", { name: "Choose order 2210" })).toBeChecked();
    expect(screen.getByRole("checkbox", { name: "Choose order 2212" })).toBeDisabled();
    expect(screen.getByText("The order is on hold.")).toBeInTheDocument();

    await next();
    const lines = screen.getByRole("table", { name: "Lines of order 2210" });
    const quantity = within(lines).getByRole("textbox", { name: "To invoice of Hylle" });
    expect(quantity).toHaveValue("5");
    expect(screen.getByTestId("remaining-1")).toHaveTextContent("0");

    // Part invoicing: two of five, three remain.
    await userEvent.clear(quantity);
    await userEvent.type(quantity, "2");
    expect(screen.getByTestId("remaining-1")).toHaveTextContent("3");
    expect(within(lines).getByText("Part invoicing")).toBeInTheDocument();
    expect(screen.getByTestId("from-orders-chosen")).toHaveTextContent("2 lines chosen");
  });

  it("previews one invoice per order or combined per customer, and sends the quantities chosen", async () => {
    const fetchMock = server();
    const { router } = await open();
    await next();
    const quantity = screen.getByRole("textbox", { name: "To invoice of Hylle" });
    await userEvent.clear(quantity);
    await userEvent.type(quantity, "2");
    await next();

    expect(screen.getByTestId("from-orders-preview")).toHaveTextContent("Acme AS: order 2210");
    expect(screen.getByText("2 invoices")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("radio", { name: "Combined per customer" }));
    expect(screen.getByText("1 invoice")).toBeInTheDocument();
    expect(screen.getByTestId("from-orders-preview")).toHaveTextContent("Acme AS: order 2210, 2211");

    await userEvent.click(screen.getByRole("button", { name: "Create the drafts" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/invoices/1001"));
    expect(sent(fetchMock, "POST").body).toEqual({
      combine: true,
      lines: [
        { orderId: 5210, lineId: 1, quantity: 2 },
        { orderId: 5211, lineId: 3, quantity: 2 },
      ],
    });
  });

  it("lists the drafts when it made several", async () => {
    server(() => jsonResponse(201, made([1001, 1002])));
    await open();
    await next();
    await next();
    await userEvent.click(screen.getByRole("button", { name: "Create the drafts" }));

    const list = await screen.findByTestId("from-orders-made");
    expect(within(list).getByRole("link", { name: "Draft 1001: order 2210" })).toHaveAttribute(
      "href",
      "/invoices/1001",
    );
    expect(within(list).getByRole("link", { name: "Draft 1002: order 2211" })).toHaveAttribute(
      "href",
      "/invoices/1002",
    );
  });

  it("says a refusal in words, naming the order line", async () => {
    server(() => refusal(409, "allocation_exceeds_open", { orderLineId: 3 }));
    await open();
    await next();
    await next();
    await userEvent.click(screen.getByRole("button", { name: "Create the drafts" }));

    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("That is more than is left to invoice of the line.");
    expect(alert).toHaveTextContent("Order 2211, line 1: Bok");
    expect(alert).not.toHaveTextContent("The server's English.");
  });

  it("speaks Norwegian: Fra ordre, Delfakturering, Samlefaktura, Gjenstår etter", async () => {
    setLanguagePreference("nb");
    try {
      server();
      renderAtHost(<FromOrdersPage canViewCustomers={false} initialCustomerId={2001} />);
      await screen.findByRole("checkbox", { name: "Velg ordre 2210" });
      await userEvent.click(screen.getByRole("button", { name: "Neste" }));
      expect(screen.getAllByRole("columnheader", { name: "Gjenstår etter" })).toHaveLength(2);
      await userEvent.click(screen.getByRole("button", { name: "Neste" }));
      expect(screen.getByRole("radio", { name: "Samlefaktura per kunde" })).toBeInTheDocument();
    } finally {
      setLanguagePreference("auto");
    }
  });
});
