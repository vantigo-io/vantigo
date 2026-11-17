import { createFileRoute } from "@tanstack/react-router";
import { ReceivedNotInvoicedRoutePage } from "./-purchasing-pages";

/** Received, not invoiced, as of a day (P41); the list and its CSV ask for purchasing:invoices-match. */
export const Route = createFileRoute("/purchasing/received-not-invoiced")({
  component: ReceivedNotInvoicedRoutePage,
});
