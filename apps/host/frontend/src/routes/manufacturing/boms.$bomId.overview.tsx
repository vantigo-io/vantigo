import { createFileRoute, notFound } from "@tanstack/react-router";
import { validateBomOverviewSearch } from "@vantigo/manufacturing-ui/lib/search";
import { BomOverviewPage } from "@vantigo/manufacturing-ui/pages/bom-overview";
import { isPositiveId } from "./-ids";

/** A bill of materials exploded (manufacturing design M15), at the quantity and site in the search. */
export const Route = createFileRoute("/manufacturing/boms/$bomId/overview")({
  beforeLoad: ({ params }) => {
    if (!isPositiveId(params.bomId)) throw notFound();
  },
  validateSearch: validateBomOverviewSearch,
  component: BomOverviewPage,
});
