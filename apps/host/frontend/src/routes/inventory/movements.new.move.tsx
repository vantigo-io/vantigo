import { createFileRoute } from "@tanstack/react-router";
import { validateNewMovementSearch } from "@vantigo/inventory-ui/lib/search";
import { NewMovePage } from "@vantigo/inventory-ui/pages/new-move";

/** A new move between the locations of a site, opened prefilled from the search's site, location and variant. */
export const Route = createFileRoute("/inventory/movements/new/move")({
  validateSearch: validateNewMovementSearch,
  component: NewMovePage,
});
