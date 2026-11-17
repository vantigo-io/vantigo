import { createFileRoute, notFound } from "@tanstack/react-router";
import { isPositiveId } from "./-ids";
import { ReceivePurchaseOrderRoutePage } from "./-purchasing-pages";

/**
 * The receive screen of a sent order (P24–P30). Beside the order's route rather
 * than under it (`$orderId_`), so it is a page of its own, not one inside the
 * order page. A path that is not a positive whole number is the layout's not-found.
 */
export const Route = createFileRoute("/purchasing/orders/$orderId_/receive")({
  beforeLoad: ({ params }) => {
    if (!isPositiveId(params.orderId)) throw notFound();
  },
  component: ReceivePurchaseOrderRoutePage,
});
