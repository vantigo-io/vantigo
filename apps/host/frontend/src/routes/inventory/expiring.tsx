import { createFileRoute } from "@tanstack/react-router";
import { validateExpiringSearch } from "@vantigo/inventory-ui/lib/search";
import { ExpiringPage } from "@vantigo/inventory-ui/pages/expiring";

/** The lots expiring soon (manufacturing phase 2 M72), filtered by the search. */
export const Route = createFileRoute("/inventory/expiring")({
  validateSearch: validateExpiringSearch,
  component: ExpiringPage,
});
