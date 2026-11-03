import { createFileRoute } from "@tanstack/react-router";
import { validateNewMovementSearch } from "@vantigo/inventory-ui/lib/search";
import { NewReceiptPage } from "@vantigo/inventory-ui/pages/new-receipt";

/** A new goods receipt, opened prefilled from the search's site, location and variant. */
export const Route = createFileRoute("/inventory/movements/new/receipt")({
  validateSearch: validateNewMovementSearch,
  component: NewReceiptPage,
});
