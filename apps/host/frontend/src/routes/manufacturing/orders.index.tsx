import { createFileRoute } from "@tanstack/react-router";
import { validateOrdersSearch } from "@vantigo/manufacturing-ui/lib/search";
import { OrdersPage } from "@vantigo/manufacturing-ui/pages/orders";

/** The manufacturing orders (manufacturing design M24), filtered by the search. */
export const Route = createFileRoute("/manufacturing/orders/")({
  validateSearch: validateOrdersSearch,
  component: OrdersPage,
});
