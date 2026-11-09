import { createFileRoute, notFound } from "@tanstack/react-router";
import { ReceiveTransferPage } from "@vantigo/inventory-ui/pages/receive-transfer";
import { isPositiveId } from "./-ids";

/** Receiving a transfer: the scan field and a line per part. A path that is not a positive whole number is the layout's not-found. */
export const Route = createFileRoute("/inventory/transfers/$transferId/receive")({
  beforeLoad: ({ params }) => {
    if (!isPositiveId(params.transferId)) throw notFound();
  },
  component: ReceiveTransferPage,
});
