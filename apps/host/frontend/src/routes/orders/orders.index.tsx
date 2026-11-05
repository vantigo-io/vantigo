import { createFileRoute } from "@tanstack/react-router";
import { OrdersRoutePage } from "./-orders-pages";

/** The orders list (orders design O18). */
export const Route = createFileRoute("/orders/orders/")({
  component: OrdersRoutePage,
});
