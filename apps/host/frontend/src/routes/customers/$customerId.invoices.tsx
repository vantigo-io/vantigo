import { createFileRoute } from "@tanstack/react-router";
import { CustomerInvoicesTab } from "./-customer-invoices-tab";

export const Route = createFileRoute("/customers/$customerId/invoices")({
  component: CustomerInvoicesTab,
});
