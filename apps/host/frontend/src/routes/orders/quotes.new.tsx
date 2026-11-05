import { createFileRoute } from "@tanstack/react-router";
import { validateNewDocumentSearch } from "./-ids";
import { NewQuoteRoutePage } from "./-orders-pages";

/** A new quote's editor, with the customer filled in when it was opened from the customer page (O31a). */
export const Route = createFileRoute("/orders/quotes/new")({
  validateSearch: validateNewDocumentSearch,
  component: NewQuoteRoutePage,
});
