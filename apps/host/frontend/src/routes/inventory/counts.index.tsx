import { createFileRoute } from "@tanstack/react-router";
import { validateCountsSearch } from "@vantigo/inventory-ui/lib/search";
import { CountsPage } from "@vantigo/inventory-ui/pages/counts";

/**
 * The counts (inventory design §7, B): the open and the finished ones, and the
 * form that starts one — opened on arrival by `?new=1`, Spotlight's Start count.
 */
export const Route = createFileRoute("/inventory/counts/")({
  validateSearch: validateCountsSearch,
  component: CountsPage,
});
