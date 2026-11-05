import { createFileRoute, notFound } from "@tanstack/react-router";
import { isPositiveId } from "./-ids";
import { OrderRoutePage } from "./-orders-pages";

/** One order. A path that is not a positive whole number is the layout's not-found. */
export const Route = createFileRoute("/orders/orders/$orderId")({
  beforeLoad: ({ params }) => {
    if (!isPositiveId(params.orderId)) throw notFound();
  },
  component: OrderRoutePage,
});
