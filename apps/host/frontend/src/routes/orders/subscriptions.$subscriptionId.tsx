import { createFileRoute, notFound } from "@tanstack/react-router";
import { isPositiveId } from "./-ids";
import { SubscriptionRoutePage } from "./-orders-pages";

/** One subscription. A path that is not a positive whole number is the layout's not-found. */
export const Route = createFileRoute("/orders/subscriptions/$subscriptionId")({
  beforeLoad: ({ params }) => {
    if (!isPositiveId(params.subscriptionId)) throw notFound();
  },
  component: SubscriptionRoutePage,
});
