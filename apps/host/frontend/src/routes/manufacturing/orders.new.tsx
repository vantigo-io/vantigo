import { createFileRoute } from "@tanstack/react-router";
import { NewOrderPage } from "@vantigo/manufacturing-ui/pages/order";

/** A new manufacturing order's form; it says so when the caller may not create one. */
export const Route = createFileRoute("/manufacturing/orders/new")({
  component: NewOrderPage,
});
