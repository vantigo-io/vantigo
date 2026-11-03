import { createFileRoute } from "@tanstack/react-router";
import { validateLookupSearch } from "@vantigo/inventory-ui/lib/search";
import { LookupPage } from "@vantigo/inventory-ui/pages/lookup";

/** The lookup's results (inventory design I28), when the text matched several or none. */
export const Route = createFileRoute("/inventory/lookup")({
  validateSearch: validateLookupSearch,
  component: LookupPage,
});
