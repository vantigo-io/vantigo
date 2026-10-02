import { ApiConflictError } from "@vantigo/frontend-api-client";
import { i18n } from "@vantigo/frontend-shell";
import { describe, expect, it } from "vitest";
import { invoicesCatalog } from "../i18n";
import { refusalMessage } from "./errors";

/**
 * Every refusal code the server can answer, read from the invoices package's
 * own Go source at test time — the `code… = "…"` constants — so a code added
 * there without words here fails this test rather than falling through to the
 * server's English detail, which an nb reader would then see.
 */
const goSources = import.meta.glob<string>("../../../../server/internal/invoices/*.go", {
  query: "?raw",
  import: "default",
  eager: true,
});
const serverCodes = [
  ...new Set(
    Object.entries(goSources)
      .filter(([file]) => !file.endsWith("_test.go"))
      .flatMap(([, source]) =>
        [...source.matchAll(/^\s*(?:const\s+)?code[A-Z]\w*\s*=\s*"([a-z_]+)"/gm)].map((m) => m[1]),
      ),
  ),
].sort();

/** The contract's own list of the codes, in InvoicesConflictProblem's description. */
const contract = Object.values(
  import.meta.glob<string>("../../../../../openapi/invoices.yaml", { query: "?raw", import: "default", eager: true }),
)[0];
// Every snake_case word in the sentence that lists them, however it is worded.
const conflictCodes = [
  ...(
    /InvoicesConflictProblem:\s*\n\s*description: '.*?code names the rule that refused (.*?)A revision conflict/.exec(
      contract ?? "",
    )?.[1] ?? ""
  ).matchAll(/\b[a-z]+(?:_[a-z]+)+\b/g),
].map((m) => m[0]);

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
  it("reads every code the server has, and every 409 code the contract names is one of them", () => {
    // 29 today: the contract names the 28 conflicts and the 503's storage_unavailable.
    expect(serverCodes.length).toBeGreaterThanOrEqual(29);
    expect(conflictCodes.length).toBeGreaterThanOrEqual(28);
    expect(conflictCodes).toContain("invoice_fully_credited");
    expect(conflictCodes.filter((code) => !serverCodes.includes(code))).toEqual([]);
  });

  it.each(serverCodes)("has a key for %s in both catalogs", (code) => {
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

  it("names the open amount a payment exceeded, written as money, in Norwegian too", () => {
    const money = (amount: number) => `kr ${amount.toFixed(2).replace(".", ",")}`;
    expect(
      refusalMessage(conflict("payment_exceeds_open", { openAmount: 24.99 }), translate("nb"), undefined, money),
    ).toBe("Betalingen er større enn utestående beløp, kr 24,99. En overbetaling kan ikke registreres.");
  });

  it("words the rate limit's 429, whose body is not a problem document", () => {
    const limited = Object.assign(new Error("Too many requests"), { status: 429, code: "rate_limited" });
    expect(refusalMessage(limited, translate("en"))).toBe(
      "Too many e-mails were sent in a short time. Wait a few minutes and try again.",
    );
    expect(refusalMessage(limited, translate("nb"))).toBe(
      "For mange e-poster er sendt på kort tid. Vent noen minutter og prøv igjen.",
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
