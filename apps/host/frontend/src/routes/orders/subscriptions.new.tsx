import { createFileRoute } from "@tanstack/react-router";
import { validateNewDocumentSearch } from "./-ids";
import { NewSubscriptionRoutePage } from "./-orders-pages";

/** A new subscription, with the customer filled in when it was opened from the customer page (O42). */
export const Route = createFileRoute("/orders/subscriptions/new")({
  validateSearch: validateNewDocumentSearch,
  component: NewSubscriptionRoutePage,
});
