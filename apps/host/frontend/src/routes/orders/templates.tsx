import { createFileRoute } from "@tanstack/react-router";
import { TemplatesPage } from "@vantigo/orders-ui/pages/templates";

/** The quote templates (orders design O23), orders:settings' as the sidebar offers them. */
export const Route = createFileRoute("/orders/templates")({
  component: TemplatesPage,
});
