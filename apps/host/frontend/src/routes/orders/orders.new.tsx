import { createFileRoute } from "@tanstack/react-router";
import { validateNewDocumentSearch } from "./-ids";
import { NewOrderRoutePage } from "./-orders-pages";

/** A new order's editor, with the customer filled in when it was opened from the customer page (O31a). */
export const Route = createFileRoute("/orders/orders/new")({
  validateSearch: validateNewDocumentSearch,
  component: NewOrderRoutePage,
});
