import { createFileRoute, notFound } from "@tanstack/react-router";
import { isPositiveId } from "./-ids";
import { QuoteRoutePage } from "./-orders-pages";

/** One quote. A path that is not a positive whole number is the layout's not-found. */
export const Route = createFileRoute("/orders/quotes/$quoteId")({
  beforeLoad: ({ params }) => {
    if (!isPositiveId(params.quoteId)) throw notFound();
  },
  component: QuoteRoutePage,
});
