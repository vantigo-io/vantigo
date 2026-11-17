import { createFileRoute } from "@tanstack/react-router";
import { PurchaseOrdersRoutePage } from "./-purchasing-pages";

/** Every purchase order, newest first (purchasing design P23). */
export const Route = createFileRoute("/purchasing/orders/")({
  component: PurchaseOrdersRoutePage,
});
