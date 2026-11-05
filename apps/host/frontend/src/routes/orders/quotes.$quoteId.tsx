import { createFileRoute, notFound } from "@tanstack/react-router";
import { QuoteEditorPage } from "@vantigo/orders-ui/pages/quote-editor";
import { isPositiveId } from "./-ids";

/** One quote. A path that is not a positive whole number is the layout's not-found. */
export const Route = createFileRoute("/orders/quotes/$quoteId")({
  beforeLoad: ({ params }) => {
    if (!isPositiveId(params.quoteId)) throw notFound();
  },
  component: QuoteEditorPage,
});
