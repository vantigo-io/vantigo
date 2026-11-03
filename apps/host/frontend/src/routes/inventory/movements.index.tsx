import { createFileRoute } from "@tanstack/react-router";
import { validateMovementsSearch } from "@vantigo/inventory-ui/lib/search";
import { MovementsPage } from "@vantigo/inventory-ui/pages/movements";

/**
 * The movements (inventory design I30), filtered by the search. The sidebar's
 * Receipts is this list with `kind=receipt` (design §13.3).
 */
export const Route = createFileRoute("/inventory/movements/")({
  validateSearch: validateMovementsSearch,
  component: MovementsPage,
});
