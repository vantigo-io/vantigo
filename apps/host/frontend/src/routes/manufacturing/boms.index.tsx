import { createFileRoute } from "@tanstack/react-router";
import { validateBomsSearch } from "@vantigo/manufacturing-ui/lib/search";
import { BomsPage } from "@vantigo/manufacturing-ui/pages/boms";

/** The bills of materials (manufacturing design M10), filtered by the search. */
export const Route = createFileRoute("/manufacturing/boms/")({
  validateSearch: validateBomsSearch,
  component: BomsPage,
});
