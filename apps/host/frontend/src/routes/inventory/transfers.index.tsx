import { createFileRoute } from "@tanstack/react-router";
import { validateTransfersSearch } from "@vantigo/inventory-ui/lib/search";
import { TransfersPage } from "@vantigo/inventory-ui/pages/transfers";

/** The transfers between sites (inventory phase 2 design §7, A), filtered by the search. */
export const Route = createFileRoute("/inventory/transfers/")({
  validateSearch: validateTransfersSearch,
  component: TransfersPage,
});
