import { createFileRoute } from "@tanstack/react-router";
import { NewSupplierInvoicePage } from "@vantigo/accounting-ui/pages/supplier-invoices";

/** A new supplier invoice, a draft until posted (accounting design A18). */
export const Route = createFileRoute("/accounting/supplier-invoices/new")({
  component: NewSupplierInvoicePage,
});
