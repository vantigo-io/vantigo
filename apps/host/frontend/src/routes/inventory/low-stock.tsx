import { createFileRoute } from "@tanstack/react-router";
import { validateLowStockSearch } from "@vantigo/inventory-ui/lib/search";
import { LowStockPage } from "@vantigo/inventory-ui/pages/low-stock";

/** The low-stock list (inventory phase 2 design §7, B), filtered to a site by the search. */
export const Route = createFileRoute("/inventory/low-stock")({
  validateSearch: validateLowStockSearch,
  component: LowStockPage,
});
