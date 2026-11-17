import { createFileRoute } from "@tanstack/react-router";
import { validateNewPurchaseOrderSearch } from "@vantigo/purchasing-ui";
import { NewPurchaseOrderRoutePage } from "./-purchasing-pages";

/** A new draft purchase order, with the supplier filled in when it was opened from a supplier's page (P15). */
export const Route = createFileRoute("/purchasing/orders/new")({
  validateSearch: validateNewPurchaseOrderSearch,
  component: NewPurchaseOrderRoutePage,
});
