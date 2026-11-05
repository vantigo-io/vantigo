import { createFileRoute, notFound } from "@tanstack/react-router";
import { OrderEditorPage } from "@vantigo/orders-ui/pages/order-editor";
import { isPositiveId } from "./-ids";

/** One order. A path that is not a positive whole number is the layout's not-found. */
export const Route = createFileRoute("/orders/orders/$orderId")({
  beforeLoad: ({ params }) => {
    if (!isPositiveId(params.orderId)) throw notFound();
  },
  component: OrderEditorPage,
});
