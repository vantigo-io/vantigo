import { createFileRoute, notFound } from "@tanstack/react-router";
import { LotPage } from "@vantigo/inventory-ui/pages/lot";
import { isPositiveId } from "./-ids";

/** One lot's page (manufacturing phase 2 M61). A path that is not a positive whole number is the layout's not-found. */
export const Route = createFileRoute("/inventory/lots/$lotId")({
  beforeLoad: ({ params }) => {
    if (!isPositiveId(params.lotId)) throw notFound();
  },
  component: LotPage,
});
