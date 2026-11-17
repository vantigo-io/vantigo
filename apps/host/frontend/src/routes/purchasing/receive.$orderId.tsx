import { createFileRoute, notFound } from "@tanstack/react-router";
import { isPositiveId } from "./-ids";
import { ReceivePurchaseOrderRoutePage } from "./-purchasing-pages";

/**
 * The receive screen of a sent order (P24–P30), under the receiving list so
 * that its guard — purchasing:receive — is the screen's too, and a holder of
 * that key alone reaches it (A47). A path that is not a positive whole number
 * is the layout's not-found.
 */
export const Route = createFileRoute("/purchasing/receive/$orderId")({
  beforeLoad: ({ params }) => {
    if (!isPositiveId(params.orderId)) throw notFound();
  },
  component: ReceivePurchaseOrderRoutePage,
});
