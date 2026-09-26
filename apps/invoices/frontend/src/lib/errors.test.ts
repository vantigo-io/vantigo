import { ApiConflictError } from "@vantigo/frontend-api-client";
import { i18n } from "@vantigo/frontend-shell";
import { describe, expect, it } from "vitest";
import { invoicesCatalog } from "../i18n";
import { refusalMessage } from "./errors";

/**
 * Every code the list, the editor, the issue dialog, the credit flow and the
 * PDF buttons can meet, and the six the settings page meets — together
 * openapi/invoices.yaml's InvoicesConflictProblem. A code without a key would
 * fall through to the server's English detail, which an nb reader then sees.
 */
const documentCodes = [
  "invoice_issued",
  "invoice_draft",
  "customer_merged",
  "customer_archived",
  "customer_blocked",
  "customer_missing",
  "invoice_changed",
  "seller_incomplete",
  "no_lines",
  "delivery_date_missing",
  "issue_date_not_allowed",
  "buyer_incomplete",
  "vat_code_inactive",
  "vat_code_not_valid",
  "vat_not_registered",
  "category_o_not_allowed",
  "reverse_charge_needs_org_number",
  "vat_codes_ambiguous",
  "credit_exceeds_line",
  "credit_exceeds_invoice",
  "credit_note_not_creditable",
  "invoice_fully_credited",
  "storage_unavailable",
];

const settingsCodes = [
  "series_locked",
  "vat_code_in_use",
  "rate_change_in_past",
  "rate_period_not_latest",
  "rate_period_last",
  "rate_period_in_use",
];

/** A refusal as the shared client throws it for a 409 problem. */
const conflict = (code: string, problem: Record<string, unknown> = {}) =>
  new ApiConflictError("The server's English.", {
    title: "Refused",
    detail: "The server's English.",
    code,
    problem: { title: "Refused", status: 409, code, detail: "The server's English.", ...problem },
  });

const translate = (language: "en" | "nb") => {
  const t = i18n.getFixedT(language, "invoices");
  return (key: string, values?: Record<string, unknown>) => t(key, values);
};

describe("a refusal's words", () => {
  it.each([...documentCodes, ...settingsCodes])("has a key for %s in both catalogs", (code) => {
    expect(invoicesCatalog.en).toHaveProperty([`refusal.${code}`]);
    expect(invoicesCatalog.nb).toHaveProperty([`refusal.${code}`]);
  });

  it("names the dates, the line and the customer merged into, in Norwegian too", () => {
    const nb = translate("nb");
    const date = (d: string) => `«${d}»`;
    expect(
      refusalMessage(conflict("issue_date_not_allowed", { allowedIssueDates: ["2026-08-31", "2026-09-12"] }), nb, date),
    ).toBe("Den fakturadatoen er ikke tillatt i dag. Den kan være: «2026-08-31», «2026-09-12».");
    expect(refusalMessage(conflict("credit_exceeds_line", { linePosition: 3 }), nb)).toBe(
      "Linje 3 krediterer mer enn den opprinnelige linjen hadde igjen.",
    );
    expect(refusalMessage(conflict("customer_merged", { mergedInto: 2002 }), nb)).toBe(
      "Kunden er slått sammen med en annen (kunde-id 2002); fakturer den i stedet.",
    );
  });

  it("words the refusals about a document's kind, never the server's English", () => {
    const en = translate("en");
    expect(refusalMessage(conflict("credit_note_not_creditable"), en)).toBe("A credit note cannot itself be credited.");
    expect(refusalMessage(conflict("invoice_draft"), en)).toBe("A draft has no document yet; preview it instead.");
  });

  it("says a code it has no words for, and an uncoded conflict, as the server does", () => {
    const en = translate("en");
    expect(refusalMessage(conflict("something_new"), en)).toBe("The server's English.");
    expect(
      refusalMessage(new Error("Invoice revision 4 is current; this change was made against revision 3"), en),
    ).toBe("Invoice revision 4 is current; this change was made against revision 3");
  });
});
