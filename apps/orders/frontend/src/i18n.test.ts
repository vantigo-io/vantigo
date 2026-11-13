import { describe, expect, it } from "vitest";
import { ordersCatalog } from "./i18n";
import { ordersNavItems } from "./lib/navigation";

// The package's own source, tests and generated files aside.
const sources = import.meta.glob(["./**/*.{ts,tsx}", "!./**/*.test.{ts,tsx}", "!./test/**", "!./api-schema.d.ts"], {
  query: "?raw",
  import: "default",
  eager: true,
}) as Record<string, string>;

/** A plural key is written as its stem; the catalog holds the `_one` and `_other` forms. */
const inCatalog = (key: string) =>
  key in ordersCatalog.en || (`${key}_one` in ordersCatalog.en && `${key}_other` in ordersCatalog.en);

describe("the orders catalog", () => {
  it("has every key in both languages, and no other", () => {
    expect(Object.keys(ordersCatalog.nb).sort()).toEqual(Object.keys(ordersCatalog.en).sort());
  });

  it("says something for every key in both languages", () => {
    for (const [language, catalog] of Object.entries(ordersCatalog)) {
      for (const [key, text] of Object.entries(catalog)) {
        expect(String(text).trim(), `${language}.${key} is empty`).not.toBe("");
      }
    }
  });

  it("keeps each key's placeholders the same in both languages", () => {
    const placeholders = (text: string) => [...text.matchAll(/\{\{(\w+)\}\}/g)].map((m) => m[1]).sort();
    for (const [key, text] of Object.entries(ordersCatalog.en))
      expect(placeholders(ordersCatalog.nb[key as keyof typeof ordersCatalog.nb]), key).toEqual(placeholders(text));
  });

  it("names every sidebar destination", () => {
    for (const item of ordersNavItems) expect(ordersCatalog.en).toHaveProperty(item.label);
  });

  it("has words for every key the source asks for by name", () => {
    const missing = new Set<string>();
    for (const [file, text] of Object.entries(sources)) {
      for (const match of text.matchAll(/\bt\("([A-Za-z0-9_.]+)"/g))
        if (!inCatalog(match[1])) missing.add(`${file}: ${match[1]}`);
      // The keys a row's check hands to `t` (lib/lines.ts).
      for (const match of text.matchAll(/problems\.set\(index, "([A-Za-z]+)"\)/g))
        if (!inCatalog(match[1])) missing.add(`${file}: ${match[1]}`);
    }
    expect([...missing]).toEqual([]);
  });

  it("words every state, action and refusal code of the contract", () => {
    const families: Record<string, readonly string[]> = {
      quoteStatus: ["draft", "sent", "accepted", "declined", "expired", "cancelled"],
      orderStatus: ["draft", "confirmed", "cancelled"],
      deliveryStatus: ["none", "partial", "delivered"],
      invoiceStatus: ["none", "partial", "invoiced"],
      mailingStatus: ["queued", "sent", "failed"],
      mailKind: ["quote", "confirmation"],
      changeAction: [
        "line_added",
        "line_removed",
        "quantity_changed",
        "price_changed",
        "discount_changed",
        "text_changed",
        "delivery_changed",
        "contact_changed",
        "reference_changed",
        "cancelled_remaining",
        "held",
        "released",
        "locked",
        "unlocked",
        "delivered_marked",
        "delivered_undone",
        "delivered_beyond_kits",
        "draft_allocations_released",
        "activated",
        "paused",
        "resumed",
        "ended",
        "cancelled",
        "effective_from",
        "notice_changed",
        "final_period",
      ],
      finalPeriod: ["full", "prorated"],
      subscriptionStatus: ["draft", "active", "paused", "ended", "cancelled"],
      periodState: ["invoiced", "draft", "skipped", "paused", "due", "upcoming"],
      interval: ["monthly", "quarterly", "half_yearly", "yearly"],
      billing: ["in_advance", "in_arrears"],
      orderInvoiceState: ["invoiced", "released"],
      // OrdersProblem's codes (openapi/orders.yaml).
      refusal: [
        "too_many_lines",
        "optional_not_allowed",
        "line_unpriced",
        "vat_code_unmapped",
        "order_empty",
        "customer_disabled",
        "project_customer_mismatch",
        "quantity_below_progress",
        "over_delivery",
        "version_conflict",
        "quote_not_draft",
        "quote_not_sent",
        "quote_already_accepted",
        "order_not_draft",
        "order_not_confirmed",
        "order_locked",
        "order_has_progress",
        "order_in_draft_invoice",
        "line_invoiced",
        "line_has_progress",
        "line_in_draft_invoice",
        "client_request_reused",
        "dependency_unavailable",
        "sending_disabled",
        // contracts.AllocationRefusal's codes, which Invoices' wizard may hand on.
        "allocation_exceeds_open",
        "line_not_invoiceable",
        "subscription_not_invoiceable",
        "nothing_due",
        // The subscriptions' (O42–O44).
        "subscription_goods_not_allowed",
        "subscription_not_draft",
        "subscription_not_active",
        "subscription_not_paused",
        "subscription_not_editable",
        "subscription_not_cancellable",
        "subscription_billed",
        "subscription_empty",
        "change_in_billed_period",
        "end_before_billed",
        "not_a_period_start",
        "final_period_billed",
      ],
    };
    for (const [family, values] of Object.entries(families))
      for (const value of values) expect(ordersCatalog.en, `${family}.${value}`).toHaveProperty([`${family}.${value}`]);
  });
});
