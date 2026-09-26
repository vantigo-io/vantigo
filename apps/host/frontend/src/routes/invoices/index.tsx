import { createFileRoute } from "@tanstack/react-router";
import { InvoicesPage } from "@vantigo/invoices-ui/pages/invoices";

export const Route = createFileRoute("/invoices/")({
  component: InvoicesPage,
});
