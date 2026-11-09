import { createFileRoute, notFound } from "@tanstack/react-router";
import { TransferPage } from "@vantigo/inventory-ui/pages/transfer";
import { isPositiveId } from "./-ids";

/** One transfer. A path that is not a positive whole number is the layout's not-found. */
export const Route = createFileRoute("/inventory/transfers/$transferId/")({
  beforeLoad: ({ params }) => {
    if (!isPositiveId(params.transferId)) throw notFound();
  },
  component: TransferPage,
});
