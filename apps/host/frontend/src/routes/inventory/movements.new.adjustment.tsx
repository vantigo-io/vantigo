import { createFileRoute } from "@tanstack/react-router";
import { validateNewMovementSearch } from "@vantigo/inventory-ui/lib/search";
import { NewAdjustmentPage } from "@vantigo/inventory-ui/pages/new-adjustment";

/** A new adjustment, opened prefilled from the search's site, location and variant. */
export const Route = createFileRoute("/inventory/movements/new/adjustment")({
  validateSearch: validateNewMovementSearch,
  component: NewAdjustmentPage,
});
