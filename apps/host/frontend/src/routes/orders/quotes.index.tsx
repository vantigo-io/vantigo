import { createFileRoute } from "@tanstack/react-router";
import { QuotesPage } from "@vantigo/orders-ui/pages/quotes";

/** The quotes list (orders design O20). */
export const Route = createFileRoute("/orders/quotes/")({
  component: QuotesPage,
});
