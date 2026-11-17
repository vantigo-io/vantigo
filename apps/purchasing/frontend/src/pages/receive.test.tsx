import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { setLanguagePreference } from "@vantigo/frontend-shell";
import { afterEach, describe, expect, it } from "vitest";
import type { PurchasingReceiveCode, PurchasingReceiveForm } from "../api/purchasing";
import { type Call, fakeServer, jsonResponse, refusal } from "../test/api";
import { commonAnswers, order, receipt, receiveForm, receiveLine } from "../test/fixtures";
import { renderRoute } from "../test/render";

const RECEIPTS = "/api/v1/purchasing/orders/501/receipts";

/** What a scanned code names: by code, the matches the server answers. */
type Codes = Record<string, PurchasingReceiveCode[]>;

const serve = (
  form: PurchasingReceiveForm,
  { codes = {}, write }: { codes?: Codes; write?: (call: Call) => Response | undefined } = {},
) =>
  fakeServer((call) => {
    if (call.method === "GET" && call.url === "/api/v1/purchasing/orders/501/receive") return jsonResponse(200, form);
    if (call.method === "GET" && call.url.startsWith("/api/v1/purchasing/orders/501/receive/code?")) {
      const code = new URL(call.url, "http://test").searchParams.get("code") ?? "";
      return jsonResponse(200, { items: codes[code] ?? [] });
    }
    if (call.method === "GET" && call.url === "/api/v1/purchasing/orders/501") return jsonResponse(200, order());
    if (call.method === "GET" && call.url === RECEIPTS) return jsonResponse(200, { items: [] });
    if (call.method === "POST" && call.url === RECEIPTS)
      return write?.(call) ?? jsonResponse(201, { order: order(), receipt: receipt() });
    return commonAnswers(call);
  });

const rows = async () => {
  await screen.findByTestId("receive-rows");
  return screen.getAllByTestId("receive-row");
};
const quantityOf = (row: HTMLElement) => within(row).getByRole("textbox", { name: /^Quantity of/ });
const submit = () => userEvent.click(screen.getByRole("button", { name: "Receive" }));
const scanField = () => screen.findByRole("textbox", { name: "Scan an item" });
const scan = async (code: string) => {
  const field = await scanField();
  await userEvent.clear(field);
  await userEvent.type(field, `${code}{Enter}`);
};
/** The body of the last receipt posted. */
const lastReceipt = (server: ReturnType<typeof fakeServer>) => (server.sent("POST", RECEIPTS)?.body ?? {}) as Sent;
type Sent = { clientRequestId: string; acceptOver?: boolean; deliveryNote?: string; note?: string; lines: unknown[] };

const twoLines = () =>
  receiveForm({
    lines: [
      receiveLine(),
      receiveLine({
        orderLineId: 12,
        position: 2,
        variantId: 303,
        sku: "BOLT-8",
        name: "Bolt 8 mm",
        open: "50",
        received: "0",
        quantity: "50",
        packagingId: null,
        packagingName: null,
        packagingQuantity: null,
        defaultLocationId: null,
      }),
    ],
  });

describe("the receive screen", () => {
  it("prefills each open goods line's remainder on its default location, shows no price, and posts one receipt", async () => {
    const server = serve(twoLines());
    const { router } = renderRoute("/purchasing/receive/501");
    const [first, second] = await rows();
    expect(screen.getByRole("heading", { name: "Receive order 3001" })).toBeInTheDocument();
    expect(quantityOf(first)).toHaveValue("800");
    expect(quantityOf(second)).toHaveValue("50");
    expect(within(first).getByRole("combobox", { name: /^Location of/ })).toHaveValue("HL-A-01");
    // The line with no default location goes on the site's own node.
    expect(within(second).getByRole("combobox", { name: /^Location of/ })).toHaveValue("HL");
    expect(screen.queryByText(/NOK|\bkr\b|45[.,]50|price/i)).not.toBeInTheDocument();
    expect(screen.getByTestId("receive-rows").textContent).not.toMatch(/NOK|\bkr\b|45[.,]50|price/i);

    await userEvent.type(screen.getByRole("textbox", { name: "Delivery note" }), "FS-123");
    await submit();
    await waitFor(() => expect(router.state.location.pathname).toBe("/purchasing/orders/501"));
    const sent = lastReceipt(server);
    expect(sent.clientRequestId).toMatch(/^[0-9a-f-]{36}$/);
    expect(sent.deliveryNote).toBe("FS-123");
    expect(sent.acceptOver).toBeUndefined();
    expect(sent.lines).toEqual([
      { orderLineId: 11, quantity: "800", locationId: 91 },
      { orderLineId: 12, quantity: "50", locationId: 90 },
    ]);
    expect(await screen.findByText("Receipt 9001 is posted.")).toBeInTheDocument();
  });

  it("splits a line over two locations, sends a packaging's count, and leaves rows at 0 out", async () => {
    const server = serve(twoLines());
    renderRoute("/purchasing/receive/501");
    const [first, second] = await rows();
    await userEvent.click(within(first).getByRole("button", { name: "Split" }));
    const [, split] = screen.getAllByTestId("receive-row");
    await userEvent.click(within(first).getByRole("radio", { name: "Eske" }));
    expect(quantityOf(first)).toHaveValue("8");
    await userEvent.clear(quantityOf(first));
    await userEvent.type(quantityOf(first), "5");
    await userEvent.clear(quantityOf(split));
    await userEvent.type(quantityOf(split), "300");
    await userEvent.click(within(split).getByRole("combobox", { name: /^Location of/ }));
    await userEvent.click(await screen.findByRole("option", { name: "HL-B-02" }));
    await userEvent.clear(quantityOf(second));
    await userEvent.type(quantityOf(second), "0");
    await submit();
    await waitFor(() => expect(server.count("POST", RECEIPTS)).toBe(1));
    expect(lastReceipt(server).lines).toEqual([
      { orderLineId: 11, quantity: "5", locationId: 91, inPackaging: true },
      { orderLineId: 11, quantity: "300", locationId: 92 },
    ]);
  });

  it("sends a lot line's number, supplier lot and dates", async () => {
    const server = serve(receiveForm({ lines: [receiveLine({ tracking: "lot", packagingId: null })] }));
    renderRoute("/purchasing/receive/501");
    const [row] = await rows();
    await userEvent.type(within(row).getByRole("textbox", { name: "Lot number" }), "L-2026-11");
    await userEvent.type(within(row).getByRole("textbox", { name: "Supplier's lot" }), "S-9");
    await userEvent.type(within(row).getByLabelText("Production date"), "2026-11-01");
    await userEvent.type(within(row).getByLabelText("Expiry"), "2027-05-31");
    await submit();
    await waitFor(() => expect(server.count("POST", RECEIPTS)).toBe(1));
    expect(lastReceipt(server).lines).toEqual([
      {
        orderLineId: 11,
        quantity: "800",
        locationId: 91,
        lot: { number: "L-2026-11", supplierLot: "S-9", productionDate: "2026-11-01", expiry: "2027-05-31" },
      },
    ]);
  });

  it("counts a serial line by its serials", async () => {
    const server = serve(receiveForm({ lines: [receiveLine({ tracking: "serial", open: "3", packagingId: null })] }));
    renderRoute("/purchasing/receive/501");
    const [row] = await rows();
    expect(screen.getByRole("button", { name: "Receive" })).toBeDisabled();
    await userEvent.type(within(row).getByRole("textbox", { name: /^Serial numbers/ }), "SN-1{Enter}SN-2");
    expect(within(row).getByText("2 stk")).toBeInTheDocument();
    await submit();
    await waitFor(() => expect(server.count("POST", RECEIPTS)).toBe(1));
    expect(lastReceipt(server).lines).toEqual([
      { orderLineId: 11, quantity: "2", locationId: 91, serials: ["SN-1", "SN-2"] },
    ]);
  });

  it("asks before receiving more than ordered, then sends the same receipt with acceptOver", async () => {
    let first = true;
    const server = serve(twoLines(), {
      write: () => {
        if (!first) return undefined;
        first = false;
        return refusal(409, "over_receipt", { lines: [{ index: 1, code: "over_receipt", detail: "Over." }] });
      },
    });
    const { router } = renderRoute("/purchasing/receive/501");
    const [, second] = await rows();
    await userEvent.clear(quantityOf(second));
    await userEvent.type(quantityOf(second), "60");
    await submit();
    const dialog = await screen.findByRole("dialog", { name: "More than ordered — receive anyway?" });
    expect(within(dialog).getByText(/BOLT-8 · Bolt 8 mm/)).toBeInTheDocument();
    const firstId = lastReceipt(server).clientRequestId;
    await userEvent.click(within(dialog).getByRole("button", { name: "Receive anyway" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/purchasing/orders/501"));
    const resent = lastReceipt(server);
    expect(server.count("POST", RECEIPTS)).toBe(2);
    expect(resent.acceptOver).toBe(true);
    expect(resent.clientRequestId).toBe(firstId);
  });

  it("marks a refused line on its row, in words", async () => {
    serve(twoLines(), {
      write: () =>
        refusal(400, "location_unknown", { lines: [{ index: 1, code: "location_unknown", detail: "No such." }] }),
    });
    renderRoute("/purchasing/receive/501");
    const [first, second] = await rows();
    await submit();
    expect(await within(second).findByTestId("row-error")).toHaveTextContent("The location is not one of the site's.");
    expect(within(first).queryByTestId("row-error")).not.toBeInTheDocument();
    expect(screen.getByTestId("receive-error")).toHaveTextContent("The location is not one of the site's.");
  });

  it("counts from 0 on the first scan: a SKU adds one, and the other lines are cleared", async () => {
    const server = serve(twoLines(), { codes: { "SKR-10": [{ orderLineId: 11, quantity: "1", packagingId: null }] } });
    renderRoute("/purchasing/receive/501");
    const [first, second] = await rows();
    await scan("SKR-10");
    await waitFor(() => expect(quantityOf(first)).toHaveValue("1"));
    expect(quantityOf(second)).toHaveValue("0");
    await scan("SKR-10");
    await waitFor(() => expect(quantityOf(first)).toHaveValue("2"));
    await submit();
    await waitFor(() => expect(server.count("POST", RECEIPTS)).toBe(1));
    expect(lastReceipt(server).lines).toEqual([{ orderLineId: 11, quantity: "2", locationId: 91 }]);
  });

  it("adds a packaging's quantity for its barcode", async () => {
    serve(twoLines(), {
      codes: { "7070000000200": [{ orderLineId: 11, quantity: "100", packagingId: 5, packagingName: "Eske" }] },
    });
    renderRoute("/purchasing/receive/501");
    const [first] = await rows();
    await scan("7070000000200");
    await waitFor(() => expect(quantityOf(first)).toHaveValue("100"));
    await scan("7070000000200");
    await waitFor(() => expect(quantityOf(first)).toHaveValue("200"));
  });

  it("reads a GS1 code: its GTIN is looked up, its lot and expiry fill the row, another lot is a new row", async () => {
    const server = serve(receiveForm({ lines: [receiveLine({ tracking: "lot", packagingId: null })] }), {
      codes: { "07070000000101": [{ orderLineId: 11, quantity: "1", packagingId: null }] },
    });
    renderRoute("/purchasing/receive/501");
    await rows();
    await scan("(01)07070000000101(17)270531(10)LOT-42");
    await waitFor(() => expect(screen.getByRole("textbox", { name: "Lot number" })).toHaveValue("LOT-42"));
    const [row] = screen.getAllByTestId("receive-row");
    expect(quantityOf(row)).toHaveValue("1");
    expect(within(row).getByLabelText("Expiry")).toHaveValue("2027-05-31");
    await scan("(01)07070000000101(17)270630(10)LOT-43");
    await waitFor(() => expect(screen.getAllByTestId("receive-row")).toHaveLength(2));
    await submit();
    await waitFor(() => expect(server.count("POST", RECEIPTS)).toBe(1));
    expect(lastReceipt(server).lines).toEqual([
      { orderLineId: 11, quantity: "1", locationId: 91, lot: { number: "LOT-42", expiry: "2027-05-31" } },
      { orderLineId: 11, quantity: "1", locationId: 91, lot: { number: "LOT-43", expiry: "2027-06-30" } },
    ]);
  });

  it("says a code that names no open line is not on this order", async () => {
    serve(twoLines());
    renderRoute("/purchasing/receive/501");
    const [first] = await rows();
    await scan("NOPE");
    expect(await screen.findByText("Not on this order")).toBeInTheDocument();
    expect(quantityOf(first)).toHaveValue("800");
    expect(await scanField()).toHaveValue("NOPE");
  });

  it("after a receipt, a receiver who cannot read orders goes back to the orders to receive", async () => {
    serve(twoLines());
    const { router } = renderRoute("/purchasing/receive/501", { canRead: false });
    await rows();
    expect(screen.getByRole("link", { name: "Cancel" })).toHaveAttribute("href", "/purchasing/receive");
    await submit();
    await waitFor(() => expect(router.state.location.pathname).toBe("/purchasing/receive"));
  });

  it("switching the unit shown keeps what was typed, and a typed packaging count is sent as typed", async () => {
    const server = serve(twoLines());
    renderRoute("/purchasing/receive/501");
    const [first] = await rows();
    await userEvent.clear(quantityOf(first));
    await userEvent.type(quantityOf(first), "150");
    await userEvent.click(within(first).getByRole("radio", { name: "Eske" }));
    expect(quantityOf(first)).toHaveValue("1.5");
    await userEvent.click(within(first).getByRole("radio", { name: "stk" }));
    expect(quantityOf(first)).toHaveValue("150");
    await userEvent.clear(quantityOf(first));
    await userEvent.type(quantityOf(first), "7");
    await userEvent.click(within(first).getByRole("radio", { name: "Eske" }));
    await userEvent.click(within(first).getByRole("radio", { name: "stk" }));
    expect(quantityOf(first)).toHaveValue("7");
    await submit();
    await waitFor(() => expect(server.count("POST", RECEIPTS)).toBe(1));
    expect(lastReceipt(server).lines[0]).toEqual({ orderLineId: 11, quantity: "7", locationId: 91 });
  });

  it("a scan on a serial line without a serial asks for the serial and clears nothing", async () => {
    serve(
      receiveForm({
        lines: [
          receiveLine({ tracking: "serial", open: "3", packagingId: null }),
          receiveLine({
            orderLineId: 12,
            position: 2,
            sku: "BOLT-8",
            name: "Bolt 8 mm",
            open: "50",
            packagingId: null,
          }),
        ],
      }),
      { codes: { "SKR-10": [{ orderLineId: 11, quantity: "1", packagingId: null }] } },
    );
    renderRoute("/purchasing/receive/501");
    const [, second] = await rows();
    await scan("SKR-10");
    expect(await screen.findByText("Scan the serial number: this code names no serial.")).toBeInTheDocument();
    expect(quantityOf(second)).toHaveValue("50");
  });

  it("a GS1 code whose lot could not be read counts the item and asks for the lot", async () => {
    serve(receiveForm({ lines: [receiveLine({ tracking: "lot", packagingId: null })] }), {
      codes: { "07070000000101": [{ orderLineId: 11, quantity: "1", packagingId: null }] },
    });
    renderRoute("/purchasing/receive/501");
    const [row] = await rows();
    await scan("010707000000010110LOT4217270531");
    expect(
      await screen.findByText("The lot could not be read from the code: type the lot number."),
    ).toBeInTheDocument();
    await waitFor(() => expect(quantityOf(row)).toHaveValue("1"));
    expect(within(row).getByRole("textbox", { name: "Lot number" })).toHaveValue("");
  });
});

describe("the receive screen in Norwegian", () => {
  afterEach(() => {
    setLanguagePreference("auto");
  });

  it("takes a decimal comma, shows one, and sends the point", async () => {
    setLanguagePreference("nb");
    const server = serve(twoLines(), { codes: { "BOLT-8": [{ orderLineId: 12, quantity: "1", packagingId: null }] } });
    renderRoute("/purchasing/receive/501");
    await screen.findByTestId("receive-rows");
    const quantity = (name: string) => screen.getByRole("textbox", { name: `Antall av ${name}` });
    await userEvent.clear(quantity("SKR-10 · Skrue 10 mm"));
    await userEvent.type(quantity("SKR-10 · Skrue 10 mm"), "250");
    const [first] = screen.getAllByTestId("receive-row");
    await userEvent.click(within(first).getByRole("radio", { name: "Eske" }));
    expect(quantity("SKR-10 · Skrue 10 mm")).toHaveValue("2,5");
    await userEvent.clear(quantity("SKR-10 · Skrue 10 mm"));
    await userEvent.type(quantity("SKR-10 · Skrue 10 mm"), "1,5");
    await userEvent.clear(quantity("BOLT-8 · Bolt 8 mm"));
    await userEvent.type(quantity("BOLT-8 · Bolt 8 mm"), "12,25");
    await userEvent.click(screen.getByRole("button", { name: "Motta" }));
    await waitFor(() => expect(server.count("POST", RECEIPTS)).toBe(1));
    expect(lastReceipt(server).lines).toEqual([
      { orderLineId: 11, quantity: "1.5", locationId: 91, inPackaging: true },
      { orderLineId: 12, quantity: "12.25", locationId: 90 },
    ]);
  });

  it("a scan after a word typed where the quantity goes writes no NaN, and counts on from a decimal comma", async () => {
    setLanguagePreference("nb");
    serve(twoLines(), { codes: { "BOLT-8": [{ orderLineId: 12, quantity: "1", packagingId: null }] } });
    renderRoute("/purchasing/receive/501");
    await screen.findByTestId("receive-rows");
    const quantity = (name: string) => screen.getByRole("textbox", { name: `Antall av ${name}` });
    await scanIn("Skann en vare", "BOLT-8");
    await waitFor(() => expect(quantity("BOLT-8 · Bolt 8 mm")).toHaveValue("1"));
    await userEvent.clear(quantity("BOLT-8 · Bolt 8 mm"));
    await userEvent.type(quantity("BOLT-8 · Bolt 8 mm"), "tolv");
    await scanIn("Skann en vare", "BOLT-8");
    await waitFor(() => expect(quantity("BOLT-8 · Bolt 8 mm")).toHaveValue("1"));
    await userEvent.clear(quantity("BOLT-8 · Bolt 8 mm"));
    await userEvent.type(quantity("BOLT-8 · Bolt 8 mm"), "0,5");
    await scanIn("Skann en vare", "BOLT-8");
    await waitFor(() => expect(quantity("BOLT-8 · Bolt 8 mm")).toHaveValue("1,5"));
  });
});

const scanIn = async (label: string, code: string) => {
  const field = await screen.findByRole("textbox", { name: label });
  await userEvent.clear(field);
  await userEvent.type(field, `${code}{Enter}`);
};
