import { createFileRoute } from "@tanstack/react-router";
import { validateNewOrderSearch } from "@vantigo/manufacturing-ui/lib/search";
import { NewOrderPage } from "@vantigo/manufacturing-ui/pages/order";

/**
 * A new manufacturing order's form; it says so when the caller may not create
 * one. `?salesOrderLineId=` prefills it from a sales order's line (M45).
 */
export const Route = createFileRoute("/manufacturing/orders/new")({
  validateSearch: validateNewOrderSearch,
  component: NewOrderPage,
});
