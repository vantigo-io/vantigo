import { createFileRoute, notFound } from "@tanstack/react-router";
import { SupplierInvoicePage } from "@vantigo/accounting-ui/pages/supplier-invoices";
import { isPositiveId } from "./-ids";

/** One supplier invoice (accounting design A18, A19). A path that is not a positive whole number is the layout's not-found. */
export const Route = createFileRoute("/accounting/supplier-invoices/$supplierInvoiceId")({
  beforeLoad: ({ params }) => {
    if (!isPositiveId(params.supplierInvoiceId)) throw notFound();
  },
  component: SupplierInvoicePage,
});
