import { createFileRoute } from "@tanstack/react-router";
import { QuotesRoutePage } from "./-orders-pages";

/** The quotes list (orders design O20). */
export const Route = createFileRoute("/orders/quotes/")({
  component: QuotesRoutePage,
});
