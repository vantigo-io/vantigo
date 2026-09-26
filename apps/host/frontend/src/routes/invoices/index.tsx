import { createFileRoute } from "@tanstack/react-router";
import { InvoicesList } from "./-invoices-list";

export const Route = createFileRoute("/invoices/")({
  component: InvoicesList,
});
