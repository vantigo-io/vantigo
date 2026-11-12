import { createFileRoute } from "@tanstack/react-router";
import { validateDeviationsSearch } from "@vantigo/manufacturing-ui/lib/search";
import { DeviationsPage } from "@vantigo/manufacturing-ui/pages/deviations";

/** The deviations (manufacturing phase 2 M99), linked to from an order, a product or a lot. */
export const Route = createFileRoute("/manufacturing/deviations/")({
  validateSearch: validateDeviationsSearch,
  component: DeviationsPage,
});
