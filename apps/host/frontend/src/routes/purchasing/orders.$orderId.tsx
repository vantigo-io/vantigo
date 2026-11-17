import { createFileRoute, notFound } from "@tanstack/react-router";
import { isPositiveId } from "./-ids";
import { PurchaseOrderRoutePage } from "./-purchasing-pages";

/** One purchase order (P15–P22). A path that is not a positive whole number is the layout's not-found. */
export const Route = createFileRoute("/purchasing/orders/$orderId")({
  beforeLoad: ({ params }) => {
    if (!isPositiveId(params.orderId)) throw notFound();
  },
  component: PurchaseOrderRoutePage,
});
