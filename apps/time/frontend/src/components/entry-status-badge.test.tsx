import { screen } from "@testing-library/react";
import { setLanguagePreference } from "@vantigo/frontend-shell";
import { describe, expect, it } from "vitest";
import { renderWithProviders } from "../test/render";
import { EntryStatusBadge } from "./entry-status-badge";

const invoicedBy = { invoiceId: 990, number: 985 };

describe("EntryStatusBadge", () => {
  // The Invoices module's stamp names its invoice (invoices work design D18);
  // the host says where invoices live when the caller may open them.
  it("names the invoice an entry was invoiced by, as a link where the host says invoices live", () => {
    renderWithProviders(
      <EntryStatusBadge status="invoiced" invoicedBy={invoicedBy} invoiceHref={(id) => `/invoices/${id}`} />,
    );
    expect(screen.getByRole("link", { name: "Invoiced by invoice 985" })).toHaveAttribute("href", "/invoices/990");
  });

  it("names it in words alone without the host's link", () => {
    renderWithProviders(<EntryStatusBadge status="invoiced" invoicedBy={invoicedBy} />);
    expect(screen.getByText("Invoiced by invoice 985")).toBeInTheDocument();
    expect(screen.queryByRole("link")).not.toBeInTheDocument();
  });

  it("says plain Invoiced for an entry marked invoiced by hand, whatever the host passes", () => {
    renderWithProviders(<EntryStatusBadge status="invoiced" invoiceHref={(id) => `/invoices/${id}`} />);
    expect(screen.getByText("Invoiced")).toBeInTheDocument();
    expect(screen.queryByRole("link")).not.toBeInTheDocument();
  });

  it("says it from the nb catalog", () => {
    setLanguagePreference("nb");
    try {
      renderWithProviders(<EntryStatusBadge status="invoiced" invoicedBy={invoicedBy} />);
      expect(screen.getByText("Fakturert på faktura 985")).toBeInTheDocument();
    } finally {
      setLanguagePreference("auto");
    }
  });
});
