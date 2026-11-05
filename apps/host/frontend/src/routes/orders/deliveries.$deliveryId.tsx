import { createFileRoute, notFound } from "@tanstack/react-router";
import { isPositiveId } from "./-ids";
import { DeliveryRoutePage } from "./-orders-pages";

/** One delivery. A path that is not a positive whole number is the layout's not-found. */
export const Route = createFileRoute("/orders/deliveries/$deliveryId")({
  beforeLoad: ({ params }) => {
    if (!isPositiveId(params.deliveryId)) throw notFound();
  },
  component: DeliveryRoutePage,
});
