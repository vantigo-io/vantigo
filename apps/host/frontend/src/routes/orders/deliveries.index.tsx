import { createFileRoute } from "@tanstack/react-router";
import { DeliveriesRoutePage } from "./-orders-pages";

/** The deliveries: what is still to deliver, and the delivery documents (orders design O47). */
export const Route = createFileRoute("/orders/deliveries/")({
  component: DeliveriesRoutePage,
});
