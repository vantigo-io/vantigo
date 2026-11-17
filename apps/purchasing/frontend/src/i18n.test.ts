import { describe, expect, it } from "vitest";
import errorsGo from "../../../server/internal/purchasing/errors.go?raw";
import { purchasingCatalog } from "./i18n";
import { purchasingNavItems } from "./lib/navigation";

// The package's own source, tests and generated files aside.
const sources = import.meta.glob(["./**/*.{ts,tsx}", "!./**/*.test.{ts,tsx}", "!./test/**", "!./api-schema.d.ts"], {
  query: "?raw",
  import: "default",
  eager: true,
}) as Record<string, string>;

/** A plural key is written as its stem; the catalog holds the `_one` and `_other` forms. */
const inCatalog = (key: string) =>
  key in purchasingCatalog.en || (`${key}_one` in purchasingCatalog.en && `${key}_other` in purchasingCatalog.en);

describe("the purchasing catalog", () => {
  it("has every key in both languages, and no other", () => {
    expect(Object.keys(purchasingCatalog.nb).sort()).toEqual(Object.keys(purchasingCatalog.en).sort());
  });

  it("says something for every key in both languages", () => {
    for (const [language, catalog] of Object.entries(purchasingCatalog)) {
      for (const [key, text] of Object.entries(catalog)) {
        expect(String(text).trim(), `${language}.${key} is empty`).not.toBe("");
      }
    }
  });

  it("keeps each key's placeholders the same in both languages", () => {
    const placeholders = (text: string) => [...text.matchAll(/\{\{(\w+)\}\}/g)].map((m) => m[1]).sort();
    for (const [key, text] of Object.entries(purchasingCatalog.en))
      expect(placeholders(purchasingCatalog.nb[key as keyof typeof purchasingCatalog.nb]), key).toEqual(
        placeholders(text),
      );
  });

  it("names every sidebar destination", () => {
    for (const item of purchasingNavItems) expect(purchasingCatalog.en).toHaveProperty(item.label);
  });

  it("has words for every key the source asks for by name", () => {
    const missing = new Set<string>();
    for (const [file, text] of Object.entries(sources)) {
      for (const match of text.matchAll(/\bt\("([A-Za-z0-9_.]+)"/g))
        if (!inCatalog(match[1])) missing.add(`${file}: ${match[1]}`);
    }
    expect([...missing]).toEqual([]);
  });

  it("words every state, action, outcome and refusal code of the contract", () => {
    const families: Record<string, readonly string[]> = {
      status: ["draft", "sent", "partially_received", "received", "closed", "cancelled"],
      mailingStatus: ["queued", "sent", "failed"],
      documentKind: ["order", "cancellation"],
      priceSetBy: ["person", "invoice", "import"],
      warning: ["below_minimum", "off_multiple"],
      changeAction: [
        "created",
        "updated",
        "line_added",
        "line_changed",
        "line_removed",
        "sent",
        "marked_sent",
        "sent_again",
        "confirmed",
        "line_closed",
        "closed",
        "reopened",
        "cancelled",
        "receipt_posted",
        "receipt_undone",
        "line_delivered",
        "line_undelivered",
      ],
      importOutcome: [
        "created",
        "updated",
        "variant_not_found",
        "variant_ambiguous",
        "packaging_not_found",
        "price_invalid",
        "quantity_invalid",
        "lead_time_invalid",
        "duplicate_row",
        "line_invalid",
      ],
      refusal: [
        "not_found",
        "supplier_not_found",
        "site_not_found",
        "site_archived",
        "currency_invalid",
        "series_start_too_low",
        "send_channel_unknown",
        "recipient_missing",
        "order_empty",
        "import_invalid",
        "too_many_lines",
        "line_invalid",
        "line_not_found",
        "variant_not_found",
        "variant_discontinued",
        "packaging_not_found",
        "quantity_invalid",
        "price_invalid",
        "discount_invalid",
        "quantity_below_received",
        "version_conflict",
        "order_not_draft",
        "order_not_sent",
        "order_not_closed",
        "order_has_receipts",
        "line_closed",
        "line_kind_changed",
        "currency_fixed",
        "supplier_inactive",
        "supplier_changed",
        "supplier_item_exists",
        "client_request_reused",
        "dependency_unavailable",
        "sending_disabled",
        "mail_failed",
        "line_not_receivable",
        "over_receipt",
        "receipt_undone",
        "receipt_invoiced",
        "line_not_deliverable",
        "lot_dates_differ",
        "receipt_unknown",
      ],
      receiptStatus: ["posted", "undone"],
    };
    for (const [family, values] of Object.entries(families))
      for (const value of values)
        expect(purchasingCatalog.en, `${family}.${value}`).toHaveProperty([`${family}.${value}`]);
  });

  it("words every refusal code the server's errors.go declares, in both languages", () => {
    const codes = [...errorsGo.matchAll(/^\s*code\w+\s*=\s*"([a-z_]+)"/gm)].map((m) => m[1]);
    expect(codes.length).toBeGreaterThan(30);
    for (const code of codes) {
      expect(purchasingCatalog.en, `en refusal.${code}`).toHaveProperty([`refusal.${code}`]);
      expect(purchasingCatalog.nb, `nb refusal.${code}`).toHaveProperty([`refusal.${code}`]);
    }
  });
});
