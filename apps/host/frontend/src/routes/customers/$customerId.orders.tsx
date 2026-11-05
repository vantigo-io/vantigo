import { createFileRoute } from "@tanstack/react-router";
import { CustomerOrdersTab } from "./-customer-orders-tab";

export const Route = createFileRoute("/customers/$customerId/orders")({
  component: CustomerOrdersTab,
});
