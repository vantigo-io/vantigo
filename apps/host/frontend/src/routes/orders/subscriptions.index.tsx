import { createFileRoute } from "@tanstack/react-router";
import { SubscriptionsRoutePage } from "./-orders-pages";

/** The subscriptions list (orders design O42). */
export const Route = createFileRoute("/orders/subscriptions/")({
  component: SubscriptionsRoutePage,
});
