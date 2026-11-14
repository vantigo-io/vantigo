import { createFileRoute, notFound } from "@tanstack/react-router";
import { SupplierPage } from "@vantigo/accounting-ui/pages/suppliers";
import { isPositiveId } from "./-ids";

/** One supplier (accounting design A17). A path that is not a positive whole number is the layout's not-found. */
export const Route = createFileRoute("/accounting/suppliers/$supplierId")({
  beforeLoad: ({ params }) => {
    if (!isPositiveId(params.supplierId)) throw notFound();
  },
  component: SupplierPage,
});
