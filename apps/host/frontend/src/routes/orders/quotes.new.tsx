import { createFileRoute } from "@tanstack/react-router";
import { QuoteEditorPage } from "@vantigo/orders-ui/pages/quote-editor";

/** A new quote's editor, with the customer filled in when it was opened from the customer page (O31a). */
export const Route = createFileRoute("/orders/quotes/new")({
  component: QuoteEditorPage,
});
