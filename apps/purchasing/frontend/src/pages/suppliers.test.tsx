import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { PurchasingMeta } from "../api/purchasing";
import { fakeServer, jsonResponse } from "../test/api";
import { commonAnswers, supplier, supplierItem } from "../test/fixtures";
import { renderProductSuppliers, renderRoute } from "../test/render";

const serveSupplier = (metaOverrides: Partial<PurchasingMeta> = {}) =>
  fakeServer((call) => {
    if (call.url === "/api/v1/purchasing/suppliers/40") return jsonResponse(200, supplier());
    if (call.method === "GET" && call.url.startsWith("/api/v1/purchasing/supplier-items?"))
      return jsonResponse(200, { items: [supplierItem({ currencyDiffers: true })], total: 1, page: 1, pageSize: 200 });
    return commonAnswers(call, metaOverrides);
  });

describe("the suppliers", () => {
  it("lists the register's suppliers with their counts, and offers the host's New supplier form", async () => {
    fakeServer((call) => commonAnswers(call));
    const { router } = renderRoute("/purchasing/suppliers");
    const rows = within(await screen.findByTestId("suppliers")).getAllByRole("row");
    expect(within(rows[1]).getByRole("link", { name: "Skruer AS" })).toHaveAttribute(
      "href",
      "/purchasing/suppliers/40",
    );
    expect(within(rows[1]).getByText("912345678")).toBeInTheDocument();

    await userEvent.click(await screen.findByRole("button", { name: "New supplier" }));
    expect(screen.getByTestId("supplier-form")).toHaveTextContent("new supplier form");
    await userEvent.click(screen.getByRole("button", { name: "save supplier" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/purchasing/suppliers/77"));
  });

  it("offers no New supplier to a caller who may not write the register", async () => {
    fakeServer((call) => commonAnswers(call, { canManageSuppliers: false }));
    renderRoute("/purchasing/suppliers");
    await screen.findByTestId("suppliers");
    expect(screen.queryByRole("button", { name: "New supplier" })).not.toBeInTheDocument();
  });

  it("shows the register's entry read-only with Accounting's link, Edit supplier and the supplier items", async () => {
    serveSupplier();
    renderRoute("/purchasing/suppliers/40");
    const entry = await screen.findByTestId("register-entry");
    expect(within(entry).getByText("Verkstedveien 1, 0150 Oslo, NO")).toBeInTheDocument();
    expect(within(entry).getByText("30 days")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Open in Accounting" })).toHaveAttribute(
      "href",
      "/accounting/suppliers/40",
    );
    expect(screen.getByRole("link", { name: "New purchase order" })).toHaveAttribute(
      "href",
      "/purchasing/orders/new?supplierId=40",
    );
    const items = within(await screen.findByTestId("supplier-items"));
    expect(items.getByText("Preferred")).toBeInTheDocument();
    expect(items.getByText("5 days")).toBeInTheDocument();
    expect(items.getByTestId("currency-differs")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Edit supplier" }));
    expect(screen.getByTestId("supplier-form")).toHaveTextContent("supplier form 40");
  });

  it("hides Accounting's link and Edit supplier without accounting:read, and the item writes without the permission", async () => {
    serveSupplier({ canOpenAccountingSuppliers: false });
    renderRoute("/purchasing/suppliers/40", { canManageSupplierItems: false });
    await screen.findByTestId("supplier-items");
    expect(screen.queryByRole("link", { name: "Open in Accounting" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Edit supplier" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Import price list" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Add supplier item" })).not.toBeInTheDocument();
  });

  it("changes a supplier item with its revision", async () => {
    const fake = fakeServer((call) => {
      if (call.method === "PUT" && call.url === "/api/v1/purchasing/supplier-items/70")
        return jsonResponse(200, supplierItem());
      if (call.url.startsWith("/api/v1/purchasing/variants?")) return jsonResponse(200, { items: [] });
      return serveAnswer(call);
    });
    renderRoute("/purchasing/suppliers/40");
    await userEvent.click(await screen.findByRole("button", { name: "Edit supplier item SKR-10" }));
    const dialog = await screen.findByRole("dialog");
    const price = within(dialog).getByRole("textbox", { name: "Price (NOK)" });
    await userEvent.clear(price);
    await userEvent.type(price, "47,25");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));
    await screen.findByText("Saved");
    expect(fake.sent("PUT", "/api/v1/purchasing/supplier-items/70")?.body).toEqual({
      revision: 3,
      supplierItemNumber: "LEV-77",
      supplierItemName: null,
      packagingId: 5,
      price: "47.25",
      leadTimeDays: 5,
      minimumQuantity: "10",
      multiple: "5",
      preferred: true,
    });
  });

  it("imports a price list: a dry run first, then the commit", async () => {
    const answers: Record<string, unknown> = {
      true: {
        ok: true,
        committed: false,
        created: 1,
        updated: 1,
        rows: [
          { row: 2, code: "SKR-10", outcome: "updated", variantId: 302 },
          { row: 3, code: "SKR-12", outcome: "created", variantId: 303 },
        ],
      },
      false: { ok: true, committed: true, created: 1, updated: 1, rows: [] },
    };
    const fake = fakeServer((call) => {
      if (call.method === "POST" && call.url.startsWith("/api/v1/purchasing/supplier-items/import?")) {
        const dry = new URL(call.url, "http://x").searchParams.get("dryRun") ?? "";
        return jsonResponse(200, answers[dry]);
      }
      return serveAnswer(call);
    });
    renderRoute("/purchasing/suppliers/40");
    await userEvent.click(await screen.findByRole("button", { name: "Import price list" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByRole("button", { name: "Import" })).toBeDisabled();
    const file = new File(["sku,price\nSKR-10,45\nSKR-12,50\n"], "prisliste.csv", { type: "text/csv" });
    await userEvent.upload(dialog.querySelector('input[type="file"]') as HTMLInputElement, file);
    await userEvent.click(within(dialog).getByRole("button", { name: "Check the file" }));
    expect(await within(dialog).findByText("1 to create, 1 to update")).toBeInTheDocument();
    await userEvent.click(within(dialog).getByRole("button", { name: "Import" }));
    await screen.findByText("1 created, 1 updated");
    expect(fake.calls.map((c) => c.url).filter((u) => u.includes("/import?"))).toEqual([
      "/api/v1/purchasing/supplier-items/import?supplierId=40&dryRun=true",
      "/api/v1/purchasing/supplier-items/import?supplierId=40&dryRun=false",
    ]);
    expect(fake.sent("POST", /\/import\?/)?.body).toBe("sku,price\nSKR-10,45\nSKR-12,50\n");
  });

  it("says why rows cannot be imported, and writes nothing", async () => {
    fakeServer((call) => {
      if (call.method === "POST")
        return jsonResponse(200, {
          ok: false,
          committed: false,
          created: 0,
          updated: 0,
          rows: [{ row: 2, code: "NOPE", outcome: "variant_not_found" }],
        });
      return serveAnswer(call);
    });
    renderRoute("/purchasing/suppliers/40");
    await userEvent.click(await screen.findByRole("button", { name: "Import price list" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.upload(
      dialog.querySelector('input[type="file"]') as HTMLInputElement,
      new File(["sku\nNOPE\n"], "p.csv", { type: "text/csv" }),
    );
    await userEvent.click(within(dialog).getByRole("button", { name: "Check the file" }));
    expect(await within(dialog).findByText("No product has this SKU or barcode")).toBeInTheDocument();
    expect(within(dialog).getByRole("button", { name: "Import" })).toBeDisabled();
  });
});

const serveAnswer = (call: { method: string; url: string }) => {
  if (call.url === "/api/v1/purchasing/suppliers/40") return jsonResponse(200, supplier());
  if (call.method === "GET" && call.url.startsWith("/api/v1/purchasing/supplier-items?"))
    return jsonResponse(200, { items: [supplierItem()], total: 1, page: 1, pageSize: 200 });
  return commonAnswers(call as Parameters<typeof commonAnswers>[0]);
};

describe("the product page's Suppliers card", () => {
  it("lists the supplier items of the product's variants, each linking to its supplier", async () => {
    const fake = fakeServer((call) =>
      call.url.startsWith("/api/v1/purchasing/supplier-items?")
        ? jsonResponse(200, { items: [supplierItem()], total: 1, page: 1, pageSize: 200 })
        : undefined,
    );
    renderProductSuppliers([302, 303]);
    const card = within(await screen.findByTestId("product-suppliers"));
    expect(await card.findByRole("link", { name: "Skruer AS" })).toHaveAttribute("href", "/purchasing/suppliers/40");
    expect(card.getByText("Preferred")).toBeInTheDocument();
    expect(fake.calls[0].url).toBe("/api/v1/purchasing/supplier-items?variantId=302&variantId=303&pageSize=200");
  });

  it("says when no supplier sells the product", async () => {
    fakeServer(() => jsonResponse(200, { items: [], total: 0, page: 1, pageSize: 200 }));
    renderProductSuppliers([302]);
    expect(await screen.findByText("No supplier sells this product yet.")).toBeInTheDocument();
  });

  it("says the suppliers' list is cut at its first 50", async () => {
    fakeServer((call) => {
      if (call.url.startsWith("/api/v1/purchasing/suppliers?"))
        return jsonResponse(200, {
          items: Array.from({ length: 50 }, (_, i) => supplier({ id: 1000 + i, number: 3000 + i })),
        });
      return commonAnswers(call);
    });
    renderRoute("/purchasing/suppliers");
    expect(await screen.findByTestId("suppliers-truncated")).toHaveTextContent(
      "Showing the first 50. Search to find the others.",
    );
  });

  it("says the supplier's items show only part of the total", async () => {
    fakeServer((call) => {
      if (call.url === "/api/v1/purchasing/suppliers/40") return jsonResponse(200, supplier());
      if (call.url.startsWith("/api/v1/purchasing/supplier-items?"))
        return jsonResponse(200, { items: [supplierItem()], total: 340, page: 1, pageSize: 200 });
      return commonAnswers(call);
    });
    renderRoute("/purchasing/suppliers/40");
    expect(await screen.findByTestId("supplier-items-truncated")).toHaveTextContent(
      "Showing 1 of 340. Search to find the others.",
    );
  });

  it("says the product card shows only part of the total", async () => {
    fakeServer(() => jsonResponse(200, { items: [supplierItem()], total: 260, page: 1, pageSize: 200 }));
    renderProductSuppliers([302]);
    expect(await screen.findByTestId("product-suppliers-truncated")).toHaveTextContent("Showing 1 of 260.");
  });
});
