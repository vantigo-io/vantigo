import { createFileRoute } from "@tanstack/react-router";
import { SupplierInvoicesPage } from "@vantigo/accounting-ui/pages/supplier-invoices";

/** The supplier invoices (accounting design A18). */
export const Route = createFileRoute("/accounting/supplier-invoices/")({
  component: SupplierInvoicesPage,
});
