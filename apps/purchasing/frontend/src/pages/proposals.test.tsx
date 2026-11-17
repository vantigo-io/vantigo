import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { PurchasingProposal, PurchasingProposalOption } from "../api/proposals";
import { fakeServer, jsonResponse, refusal } from "../test/api";
import { commonAnswers } from "../test/fixtures";
import { renderRoute } from "../test/render";

const option = (overrides: Partial<PurchasingProposalOption> = {}): PurchasingProposalOption => ({
  supplierItemId: 900,
  supplierId: 40,
  supplierName: "Skruer AS",
  preferred: true,
  supplierItemNumber: "SK-1",
  currency: "NOK",
  currencyDiffers: false,
  price: "12.5000",
  packagingId: 7,
  packagingName: "Eske",
  packagingQuantity: "10",
  minimumQuantity: "3",
  multiple: "2",
  leadTimeDays: 7,
  orderQuantity: "4",
  quantity: "40",
  orderBy: "2099-01-01",
  ...overrides,
});

const proposal = (overrides: Partial<PurchasingProposal> = {}): PurchasingProposal => ({
  variantId: 101,
  sku: "BOLT-M8",
  productName: "Bolt M8",
  unit: "pcs",
  productStatus: "Active",
  siteId: 1,
  siteName: "Hovedlager",
  source: "low_stock",
  computedAt: "2026-11-17T08:00:00Z",
  need: "33",
  onDrafts: "10",
  sentSince: "0",
  remaining: "23",
  neededOn: null,
  options: [
    option(),
    option({
      supplierItemId: 901,
      supplierId: 41,
      supplierName: "Bolter AS",
      preferred: false,
      currency: "EUR",
      price: "1.1000",
      packagingId: null,
      packagingName: null,
      packagingQuantity: "1",
      orderQuantity: "23",
      quantity: "23",
      minimumQuantity: null,
      multiple: null,
      leadTimeDays: null,
    }),
  ],
  ...overrides,
});

const serve = (rows: PurchasingProposal[], onCreate?: (body: unknown) => Response) =>
  fakeServer((call) => {
    if (call.method === "GET" && call.url.startsWith("/api/v1/purchasing/proposals"))
      return jsonResponse(200, { source: "low_stock", rows });
    if (call.method === "POST" && call.url === "/api/v1/purchasing/proposals/orders")
      return onCreate ? onCreate(call.body) : jsonResponse(200, { orders: [] });
    return commonAnswers(call);
  });

describe("the purchase proposals", () => {
  it("lists each proposal with what is netted, the preferred supplier's rounding and price, and No supplier apart", async () => {
    serve([
      proposal(),
      proposal({
        variantId: 104,
        sku: "NUT-M8",
        productName: "Mutter M8",
        onDrafts: "0",
        remaining: "4",
        need: "4",
        options: [],
      }),
    ]);
    renderRoute("/purchasing/proposals");

    const rows = within(await screen.findByTestId("proposals")).getAllByRole("row");
    expect(rows).toHaveLength(2);
    expect(within(rows[1]).getByText("BOLT-M8")).toBeInTheDocument();
    expect(within(rows[1]).getByText("23 pcs")).toBeInTheDocument();
    expect(within(rows[1]).getByText("33 needed, 10 on drafts, 0 ordered since")).toBeInTheDocument();
    expect(within(rows[1]).getByRole("textbox", { name: "Quantity of BOLT-M8 at Hovedlager" })).toHaveValue("4");
    expect(within(rows[1]).getByText("40 pcs")).toBeInTheDocument();
    expect(within(rows[1]).getByText("SK-1")).toBeInTheDocument();
    expect(screen.getByText("From Inventory's low stock.")).toBeInTheDocument();
    const none = within(screen.getByTestId("proposals-no-supplier")).getAllByRole("row");
    expect(within(none[1]).getByText("NUT-M8")).toBeInTheDocument();
    expect(screen.queryByRole("checkbox", { name: "Select NUT-M8 at Hovedlager" })).not.toBeInTheDocument();
  });

  it("switches the supplier, takes its rounding, and creates orders with the ticked rows and one request id", async () => {
    let sent: { clientRequestId: string; rows: unknown[] } | undefined;
    const fake = serve(
      [proposal(), proposal({ variantId: 102, sku: "SCREW", siteId: 2, siteName: "Butikklager" })],
      (body) => {
        sent = body as typeof sent;
        return jsonResponse(200, {
          orders: [
            {
              orderId: 777,
              supplierId: 41,
              supplierName: "Bolter AS",
              siteId: 1,
              created: true,
              linesAdded: 1,
              unpricedRows: [],
            },
          ],
        });
      },
    );
    renderRoute("/purchasing/proposals");
    await screen.findByTestId("proposals");
    const create = screen.getByRole("button", { name: "Create orders" });
    expect(create).toBeDisabled();

    await userEvent.click(screen.getByRole("combobox", { name: "Supplier of BOLT-M8 at Hovedlager" }));
    await userEvent.click(await screen.findByRole("option", { name: "Bolter AS" }));
    const quantity = screen.getByRole("textbox", { name: "Quantity of BOLT-M8 at Hovedlager" });
    expect(quantity).toHaveValue("23");
    await userEvent.clear(quantity);
    await userEvent.type(quantity, "25");
    await userEvent.click(screen.getByRole("checkbox", { name: "Select BOLT-M8 at Hovedlager" }));
    await userEvent.click(screen.getByRole("button", { name: "Create orders (1 row)" }));

    await waitFor(() => expect(sent).toBeDefined());
    expect(sent?.rows).toEqual([
      { variantId: 101, siteId: 1, supplierItemId: 901, orderQuantity: "25", remaining: "23" },
    ]);
    expect(sent?.clientRequestId).toMatch(/^[0-9a-f-]{36}$/);
    expect(await screen.findByRole("link", { name: "Draft for Bolter AS" })).toHaveAttribute(
      "href",
      "/purchasing/orders/777",
    );
    expect(fake.count("POST", "/api/v1/purchasing/proposals/orders")).toBe(1);
  });

  it("refuses a quantity that is not one, and says a row gone since in words and reads the list again", async () => {
    const fake = serve([proposal()], () =>
      refusal(409, "proposal_gone", { lines: [{ index: 0, code: "proposal_gone" }] }),
    );
    renderRoute("/purchasing/proposals");
    await screen.findByTestId("proposals");
    await userEvent.click(screen.getByRole("checkbox", { name: "Select BOLT-M8 at Hovedlager" }));
    const quantity = screen.getByRole("textbox", { name: "Quantity of BOLT-M8 at Hovedlager" });
    await userEvent.clear(quantity);
    await userEvent.type(quantity, "1.2345");
    expect(screen.getByRole("button", { name: "Create orders (1 row)" })).toBeDisabled();
    await userEvent.clear(quantity);
    await userEvent.type(quantity, "4");
    const reads = fake.count("GET", /\/api\/v1\/purchasing\/proposals/);
    await userEvent.click(screen.getByRole("button", { name: "Create orders (1 row)" }));
    // Said above the list, and on the row.
    expect(
      await screen.findAllByText(
        "Some rows are no longer proposed as shown: they are on a draft, were ordered or changed since. The list is loaded again.",
      ),
    ).toHaveLength(2);
    await waitFor(() => expect(fake.count("GET", /\/api\/v1\/purchasing\/proposals/)).toBeGreaterThan(reads));
  });

  it("asks for the variant and site a link in names, offers Show all, and nothing to tick without purchasing:orders-manage", async () => {
    const fake = serve([proposal()]);
    renderRoute("/purchasing/proposals?variantId=101&siteId=1", { canManageOrders: false });
    await screen.findByTestId("proposals");
    expect(fake.calls.some((c) => c.url === "/api/v1/purchasing/proposals?siteId=1&variantId=101")).toBe(true);
    expect(screen.getByRole("link", { name: "Show all" })).toHaveAttribute("href", "/purchasing/proposals");
    expect(screen.queryByRole("checkbox")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Create orders" })).not.toBeInTheDocument();
    expect(screen.getByText("4 Eske")).toBeInTheDocument();
  });

  it("says when there is nothing to buy", async () => {
    serve([]);
    renderRoute("/purchasing/proposals");
    expect(await screen.findByText("Nothing to buy")).toBeInTheDocument();
  });
  it("sends the same request id again when the same rows are sent after a failed answer", async () => {
    const ids: string[] = [];
    serve([proposal()], (body) => {
      ids.push((body as { clientRequestId: string }).clientRequestId);
      return ids.length === 1
        ? refusal(503, "dependency_unavailable")
        : jsonResponse(200, {
            orders: [
              {
                orderId: 9,
                supplierId: 40,
                supplierName: "Skruer AS",
                siteId: 1,
                created: true,
                linesAdded: 1,
                unpricedRows: [],
              },
            ],
          });
    });
    renderRoute("/purchasing/proposals");
    await screen.findByTestId("proposals");
    await userEvent.click(screen.getByRole("checkbox", { name: "Select BOLT-M8 at Hovedlager" }));
    await userEvent.click(screen.getByRole("button", { name: "Create orders (1 row)" }));
    await screen.findByText("A module Purchasing reads is not available.");
    await userEvent.click(screen.getByRole("button", { name: "Create orders (1 row)" }));
    await screen.findByRole("link", { name: "Draft for Skruer AS" });
    expect(ids).toHaveLength(2);
    expect(ids[1]).toBe(ids[0]);
  });

  it("reads a decimal comma as the point and sends it so", async () => {
    let sent: { rows: { orderQuantity: string }[] } | undefined;
    serve([proposal()], (body) => {
      sent = body as typeof sent;
      return jsonResponse(200, { orders: [] });
    });
    renderRoute("/purchasing/proposals");
    await screen.findByTestId("proposals");
    await userEvent.click(screen.getByRole("checkbox", { name: "Select BOLT-M8 at Hovedlager" }));
    const quantity = screen.getByRole("textbox", { name: "Quantity of BOLT-M8 at Hovedlager" });
    await userEvent.clear(quantity);
    await userEvent.type(quantity, "2,5");
    expect(screen.getByText("25 pcs")).toBeInTheDocument();
    const create = screen.getByRole("button", { name: "Create orders (1 row)" });
    expect(create).toBeEnabled();
    await userEvent.click(create);
    await waitFor(() => expect(sent).toBeDefined());
    expect(sent?.rows[0].orderQuantity).toBe("2.5");
  });

  it("drops a chosen supplier no longer offered after a reload: the typed quantity goes and the row is unticked", async () => {
    let rows = [proposal()];
    fakeServer((call) => {
      if (call.method === "GET" && call.url.startsWith("/api/v1/purchasing/proposals"))
        return jsonResponse(200, { source: "low_stock", rows });
      if (call.method === "POST" && call.url === "/api/v1/purchasing/proposals/orders") {
        rows = [proposal({ options: [option()] })];
        return refusal(409, "proposal_gone", { lines: [{ index: 0, code: "proposal_gone" }] });
      }
      return commonAnswers(call);
    });
    renderRoute("/purchasing/proposals");
    await screen.findByTestId("proposals");
    await userEvent.click(screen.getByRole("combobox", { name: "Supplier of BOLT-M8 at Hovedlager" }));
    await userEvent.click(await screen.findByRole("option", { name: "Bolter AS" }));
    const quantity = screen.getByRole("textbox", { name: "Quantity of BOLT-M8 at Hovedlager" });
    await userEvent.clear(quantity);
    await userEvent.type(quantity, "25");
    await userEvent.click(screen.getByRole("checkbox", { name: "Select BOLT-M8 at Hovedlager" }));
    await userEvent.click(screen.getByRole("button", { name: "Create orders (1 row)" }));
    // The list comes back with Skruer AS alone.
    await waitFor(() =>
      expect(screen.getByRole("textbox", { name: "Quantity of BOLT-M8 at Hovedlager" })).toHaveValue("4"),
    );
    expect(screen.getByRole("checkbox", { name: "Select BOLT-M8 at Hovedlager" })).not.toBeChecked();
    expect(screen.getByRole("button", { name: "Create orders" })).toBeDisabled();
  });

  it("lists a discontinued product apart, marked so, with nothing to tick", async () => {
    serve([proposal({ productStatus: "Discontinued", options: [] })]);
    renderRoute("/purchasing/proposals");
    const none = within(await screen.findByTestId("proposals-no-supplier")).getAllByRole("row");
    expect(within(none[1]).getByText("BOLT-M8")).toBeInTheDocument();
    expect(within(none[1]).getByText("Discontinued")).toBeInTheDocument();
    expect(screen.queryByRole("checkbox")).not.toBeInTheDocument();
  });

  it("says which drafts got lines with no price", async () => {
    serve([proposal()], () =>
      jsonResponse(200, {
        orders: [
          {
            orderId: 9,
            supplierId: 41,
            supplierName: "Bolter AS",
            siteId: 1,
            created: false,
            linesAdded: 1,
            unpricedRows: [0],
          },
        ],
      }),
    );
    renderRoute("/purchasing/proposals");
    await screen.findByTestId("proposals");
    await userEvent.click(screen.getByRole("checkbox", { name: "Select BOLT-M8 at Hovedlager" }));
    await userEvent.click(screen.getByRole("button", { name: "Create orders (1 row)" }));
    expect(
      await screen.findByText("1 line got no price: its supplier item's price is not in the draft's currency."),
    ).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Draft for Bolter AS" })).toBeInTheDocument();
  });
});
