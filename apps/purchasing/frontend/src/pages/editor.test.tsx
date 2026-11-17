import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { fakeServer, jsonResponse, refusal } from "../test/api";
import { commonAnswers, draft, line, order, variantOption } from "../test/fixtures";
import { renderRoute } from "../test/render";

const variants = (call: { url: string }) =>
  call.url.startsWith("/api/v1/purchasing/variants?") ? jsonResponse(200, { items: [variantOption()] }) : undefined;

describe("the purchase order editor", () => {
  it("fills a picked product's line from the supplier's item, warns off its multiple and creates the draft", async () => {
    const fake = fakeServer((call) => {
      if (call.method === "POST" && call.url === "/api/v1/purchasing/orders")
        return jsonResponse(201, draft({ id: 900 }));
      if (call.url === "/api/v1/purchasing/orders/900") return jsonResponse(200, draft({ id: 900 }));
      return variants(call) ?? commonAnswers(call);
    });
    const { router } = renderRoute("/purchasing/orders/new?supplierId=40");
    await screen.findByRole("heading", { name: "New purchase order" });

    await userEvent.type(screen.getByRole("combobox", { name: "Add product" }), "SKR");
    await userEvent.click(await screen.findByRole("option", { name: "SKR-10 · Skrue 10 mm" }));
    const lines = within(screen.getByTestId("editor-lines"));
    expect(lines.getByRole("textbox", { name: "The supplier's item number 1" })).toHaveValue("LEV-77");
    expect(lines.getByRole("textbox", { name: "Unit price 1" })).toHaveValue("45.5");
    expect(lines.getByRole("textbox", { name: "Quantity 1" })).toHaveValue("10");
    expect(lines.getByRole("combobox", { name: "Purchase packaging 1" })).toHaveValue("Eske (100 stk)");
    expect(fake.calls.some((c) => c.url.includes("supplierId=40"))).toBe(true);

    const qty = lines.getByRole("textbox", { name: "Quantity 1" });
    await userEvent.clear(qty);
    await userEvent.type(qty, "12");
    expect(lines.getByText("Not a multiple of 5")).toBeInTheDocument();
    await userEvent.clear(qty);
    await userEvent.type(qty, "5");
    expect(lines.getByText("Below the supplier's minimum of 10")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Create the draft" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/purchasing/orders/900"));
    const body = fake.sent("POST", "/api/v1/purchasing/orders")?.body as Record<string, unknown>;
    expect(body).toMatchObject({ supplierId: 40, buyerId: "00000000-0000-4000-8000-000000000001" });
    expect(body.lines).toEqual([
      {
        kind: "variant",
        variantId: 302,
        orderQuantity: "5",
        unitPrice: "45.5",
        supplierItemNumber: "LEV-77",
        packagingId: 5,
        expectedOn: expect.stringMatching(/^\d{4}-\d{2}-\d{2}$/),
      },
    ]);
  });

  it("names every saved line of a sent order by its id and keeps its packaging, adding a text line", async () => {
    const read = order();
    const fake = fakeServer((call) => {
      if (call.method === "GET" && call.url === "/api/v1/purchasing/orders/501") return jsonResponse(200, read);
      if (call.method === "PUT" && call.url === "/api/v1/purchasing/orders/501") return jsonResponse(200, read);
      return commonAnswers(call);
    });
    renderRoute("/purchasing/orders/501");
    await userEvent.click(await screen.findByRole("button", { name: "Edit" }));
    const lines = within(await screen.findByTestId("editor-lines"));
    expect(screen.getByRole("textbox", { name: "Currency" })).toBeDisabled();
    // A saved line of a sent order is closed on the order page, never removed.
    expect(lines.queryByRole("button", { name: "Remove the line 1" })).not.toBeInTheDocument();
    const price = lines.getByRole("textbox", { name: "Unit price 1" });
    await userEvent.clear(price);
    await userEvent.type(price, "44");
    await userEvent.click(screen.getByRole("button", { name: "Add text line" }));
    await userEvent.type(lines.getByRole("textbox", { name: "Description 2" }), "Frakt");
    await userEvent.type(lines.getByRole("textbox", { name: "Unit price 2" }), "250");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    await screen.findByText("Saved");
    const body = fake.sent("PUT", "/api/v1/purchasing/orders/501")?.body as Record<string, unknown>;
    expect(body).toMatchObject({ expectedUpdatedAt: "2026-11-16T09:00:00Z", supplierId: 40, siteId: 1 });
    expect(body.lines).toEqual([
      {
        id: 11,
        kind: "variant",
        variantId: 302,
        orderQuantity: "10",
        unitPrice: "44",
        discountPercent: "0",
        expectedOn: "2026-11-20",
        supplierItemNumber: "LEV-77",
      },
      { kind: "text", description: "Frakt", unit: null, orderQuantity: "1", unitPrice: "250", expectedOn: null },
    ]);
  });

  it("marks the line a refusal names", async () => {
    const read = order({ lines: [line({ received: "500" })] });
    fakeServer((call) => {
      if (call.method === "GET" && call.url === "/api/v1/purchasing/orders/501") return jsonResponse(200, read);
      if (call.method === "PUT")
        return refusal(400, "quantity_below_received", {
          lines: [{ index: 0, code: "quantity_below_received", detail: "x" }],
        });
      return commonAnswers(call);
    });
    renderRoute("/purchasing/orders/501");
    await userEvent.click(await screen.findByRole("button", { name: "Edit" }));
    await userEvent.click(await screen.findByRole("button", { name: "Save" }));
    const words = "The quantity cannot be less than what is received.";
    await waitFor(() => expect(screen.getAllByText(words)).toHaveLength(2));
  });

  it("offers no editor without Manage purchase orders, only why", async () => {
    const fake = fakeServer((call) => variants(call) ?? commonAnswers(call));
    renderRoute("/purchasing/orders/new?supplierId=40", { canManageOrders: false });
    expect(
      await screen.findByText(
        "Writing purchase orders needs the permission Manage purchase orders. Ask an administrator for it.",
      ),
    ).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Create the draft" })).not.toBeInTheDocument();
    expect(fake.count("POST", "/api/v1/purchasing/orders")).toBe(0);
  });

  it("offers Load again on a version conflict, which reads the order afresh", async () => {
    const read = order();
    const fake = fakeServer((call) => {
      if (call.method === "GET" && call.url === "/api/v1/purchasing/orders/501") return jsonResponse(200, read);
      if (call.method === "PUT") return refusal(409, "version_conflict");
      return commonAnswers(call);
    });
    renderRoute("/purchasing/orders/501");
    await userEvent.click(await screen.findByRole("button", { name: "Edit" }));
    await userEvent.click(await screen.findByRole("button", { name: "Save" }));
    expect(
      await screen.findByText("Somebody changed the order since you opened it. Load it again and repeat your change."),
    ).toBeInTheDocument();
    const reads = fake.count("GET", "/api/v1/purchasing/orders/501");
    await userEvent.click(screen.getByRole("button", { name: "Load again" }));
    await waitFor(() => expect(fake.count("GET", "/api/v1/purchasing/orders/501")).toBeGreaterThan(reads));
  });
});
