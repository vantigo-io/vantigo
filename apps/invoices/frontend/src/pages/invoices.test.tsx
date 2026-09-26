import { screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { InvoicesMeta } from "../api/meta";
import { jsonResponse, problemResponse } from "../test/api";
import { stubFetch } from "../test/fetch";
import { renderWithProviders } from "../test/render";
import { InvoicesPage } from "./invoices";

/** GET /meta for a fresh installation, as the server sends it — a wire literal. */
const freshMeta: InvoicesMeta = {
  currency: "NOK",
  defaultPaymentTermsDays: 14,
  sellerComplete: false,
  missingSellerFields: ["legalName", "organisationNumber", "addressLine1", "postalCode", "city", "bankAccount"],
  anythingIssued: false,
  seriesStart: 1,
  storageAvailable: true,
  today: "2026-09-26",
  vatCodes: [{ id: 1, code: "3", name: "Utgående mva 25 %", safTCode: "3", ehfCategory: "S", ratePercent: 25 }],
  capabilities: { canCreate: false, canIssue: false, canManage: false },
};

describe("the Invoices app's home", () => {
  it("mounts, reads meta and says there is nothing yet", async () => {
    const fetchMock = stubFetch((input: RequestInfo | URL) =>
      String(input) === "/api/v1/invoices/meta" ? jsonResponse(200, freshMeta) : new Response(null, { status: 404 }),
    );

    renderWithProviders(<InvoicesPage />);

    expect(await screen.findByText("No invoices yet")).toBeInTheDocument();
    expect(fetchMock.actualCalls.some(([url]) => String(url) === "/api/v1/invoices/meta")).toBe(true);
  });

  it("says when meta could not be loaded", async () => {
    stubFetch(() => problemResponse(500, "Internal Server Error"));

    renderWithProviders(<InvoicesPage />);

    expect(await screen.findByText("Could not load Invoices")).toBeInTheDocument();
  });
});
