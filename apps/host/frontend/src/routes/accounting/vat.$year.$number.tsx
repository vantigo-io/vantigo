import { createFileRoute, notFound } from "@tanstack/react-router";
import { VatTermPage } from "@vantigo/accounting-ui/pages/vat";
import { isPositiveId } from "./-ids";

/** One VAT term (accounting design A22, A23). A year or a number that is not a positive whole number is the layout's not-found. */
export const Route = createFileRoute("/accounting/vat/$year/$number")({
  beforeLoad: ({ params }) => {
    if (!isPositiveId(params.year) || !isPositiveId(params.number)) throw notFound();
  },
  component: VatTermPage,
});
