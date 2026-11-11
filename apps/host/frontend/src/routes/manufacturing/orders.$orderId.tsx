import { createFileRoute, notFound } from "@tanstack/react-router";
import { OrderPage } from "@vantigo/manufacturing-ui/pages/order";
import { isPositiveId } from "./-ids";

/**
 * One manufacturing order (manufacturing design M17–M26), where Inventory's
 * production movements link. A path that is not a positive whole number is
 * the layout's not-found.
 */
export const Route = createFileRoute("/manufacturing/orders/$orderId")({
  beforeLoad: ({ params }) => {
    if (!isPositiveId(params.orderId)) throw notFound();
  },
  component: OrderPage,
});
