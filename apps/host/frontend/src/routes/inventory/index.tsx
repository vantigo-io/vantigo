import { createFileRoute } from "@tanstack/react-router";
import { InventoryHomePage } from "@vantigo/inventory-ui/pages/home";

/**
 * The app's home (inventory design §7): the lookup, autofocused, and the
 * sites. It is `inventory:locations-view`'s or `inventory:stock-view`'s, which
 * is what the guard asks of the whole prefix; the spotlight's Find location
 * or item lands here.
 */
export const Route = createFileRoute("/inventory/")({
  component: InventoryHomePage,
});
