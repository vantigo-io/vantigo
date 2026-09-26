import { createFileRoute } from "@tanstack/react-router";
import { JournalPage } from "@vantigo/invoices-ui/pages/journal";

export const Route = createFileRoute("/invoices/journal")({
  component: JournalPage,
});
