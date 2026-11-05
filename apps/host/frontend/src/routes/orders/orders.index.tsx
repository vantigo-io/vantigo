import { createFileRoute } from "@tanstack/react-router";
import { OrdersPage } from "@vantigo/orders-ui/pages/orders";

/** The orders list (orders design O18). */
export const Route = createFileRoute("/orders/orders/")({
  component: OrdersPage,
});
