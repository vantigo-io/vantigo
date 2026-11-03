import { createFileRoute, notFound } from "@tanstack/react-router";
import { VariantStockPage } from "@vantigo/inventory-ui/pages/variant";
import { isPositiveId } from "./-ids";

/** One variant's stock by site and location, and its movements; Products' delete dialog links here. A path that is not a positive whole number is the layout's not-found. */
export const Route = createFileRoute("/inventory/variants/$variantId")({
  beforeLoad: ({ params }) => {
    if (!isPositiveId(params.variantId)) throw notFound();
  },
  component: VariantStockPage,
});
