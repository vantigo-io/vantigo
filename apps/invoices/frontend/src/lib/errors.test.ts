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

  it("names the rate and half-year a run lacks, and the bank data's latest booking, in Norwegian too", () => {
    const date = (d: string) => `«${d}»`;
    expect(
      refusalMessage(
        conflict("collection_rates_outdated", { kind: "b2b_compensation_nok", halfYear: "2027-H1" }),
        translate("en"),
      ),
    ).toBe(
      "A letter of this run needs the business compensation for 2027-H1, and there is none. Add the rate, or wait for the release that brings it. No letter was made.",
    );
    expect(
      refusalMessage(
        conflict("collection_rates_outdated", { kind: "late_interest_percent", halfYear: "2027-H1" }),
        translate("nb"),
      ),
    ).toBe(
      "Et brev i denne kjøringen trenger forsinkelsesrenten for 2027-H1, og den finnes ikke. Legg inn satsen, eller vent på utgivelsen som bringer den. Ingen brev ble laget.",
    );
    expect(refusalMessage(conflict("bank_import_stale", { lastBookedOn: "2026-08-30" }), translate("nb"), date)).toBe(
      "Den siste bankbokføringen som er importert, er fra «2026-08-30», og brev i denne kjøringen krever gebyr eller renter. Importer den siste bankfilen, eller bekreft kjøringen i boksen under.",
    );
    expect(refusalMessage(conflict("bank_import_stale"), translate("en"), date)).toBe(
      "No bank file was ever imported, and letters of this run claim charges. Import the latest bank file, or confirm the run with the box below.",
    );
  });

  it("words the rate limiter's rate_limited, whose body is not a problem document", () => {
    const limited = Object.assign(new Error("Too many requests"), { status: 429, code: "rate_limited" });
    expect(refusalMessage(limited, translate("en"))).toBe(
      "Too many requests in a short time; wait ten minutes and try again.",
    );
    expect(refusalMessage(limited, translate("nb"))).toBe(
      "For mange forespørsler på kort tid; vent ti minutter og prøv igjen.",
    );
  });

  it("words a failed send as one that may still have arrived, in Norwegian too", () => {
    expect(refusalMessage(conflict("mail_failed"), translate("en"))).toBe(
      "The mail server did not confirm the e-mail. Nothing was recorded; it may still have arrived. Check with the customer before sending again.",
    );
    expect(refusalMessage(conflict("mail_failed"), translate("nb"))).toBe(
      "E-postserveren bekreftet ikke sendingen. Ingenting ble registrert; den kan likevel ha kommet fram. Sjekk med kunden før du sender på nytt.",
    );
  });

  it("words a 429 by its code only: one without rate_limited says its own message", () => {
    const other = Object.assign(new Error("Slow down"), { status: 429 });
    expect(refusalMessage(other, translate("en"))).toBe("Slow down");
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
