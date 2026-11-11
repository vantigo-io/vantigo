import { createFileRoute } from "@tanstack/react-router";
import { validateWorkCentersSearch } from "@vantigo/manufacturing-ui/lib/search";
import { WorkCentersPage } from "@vantigo/manufacturing-ui/pages/work-centers";

/** The work centers (manufacturing design M8), filtered by the search. */
export const Route = createFileRoute("/manufacturing/work-centers/")({
  validateSearch: validateWorkCentersSearch,
  component: WorkCentersPage,
});
