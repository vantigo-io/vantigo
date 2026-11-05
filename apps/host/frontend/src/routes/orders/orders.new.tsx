import { createFileRoute } from "@tanstack/react-router";
import { OrderEditorPage } from "@vantigo/orders-ui/pages/order-editor";

/** A new order's editor, with the customer filled in when it was opened from the customer page (O31a). */
export const Route = createFileRoute("/orders/orders/new")({
  component: OrderEditorPage,
});
