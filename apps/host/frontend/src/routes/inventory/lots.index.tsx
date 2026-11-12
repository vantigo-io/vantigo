import { createFileRoute } from "@tanstack/react-router";
import { validateLotsSearch } from "@vantigo/inventory-ui/lib/search";
import { LotsPage } from "@vantigo/inventory-ui/pages/lots";

/** The lots (manufacturing phase 2 M60), filtered by the search. */
export const Route = createFileRoute("/inventory/lots/")({
  validateSearch: validateLotsSearch,
  component: LotsPage,
});
