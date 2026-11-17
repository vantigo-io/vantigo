import { createFileRoute, notFound } from "@tanstack/react-router";
import { isPositiveId } from "./-ids";
import { SupplierRoutePage } from "./-purchasing-pages";

/** One supplier: the register's entry and its supplier items (P13). A path that is not a positive whole number is the layout's not-found. */
export const Route = createFileRoute("/purchasing/suppliers/$supplierId")({
  beforeLoad: ({ params }) => {
    if (!isPositiveId(params.supplierId)) throw notFound();
  },
  component: SupplierRoutePage,
});
