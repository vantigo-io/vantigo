import { createFileRoute } from "@tanstack/react-router";
import { validateNewTransferSearch } from "@vantigo/inventory-ui/lib/search";
import { NewTransferPage } from "@vantigo/inventory-ui/pages/new-transfer";

/** A new transfer's draft form (inventory phase 2 design §7, A), opened with its sites. */
export const Route = createFileRoute("/inventory/transfers/new")({
  validateSearch: validateNewTransferSearch,
  component: NewTransferPage,
});
