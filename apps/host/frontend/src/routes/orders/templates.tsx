import { createFileRoute } from "@tanstack/react-router";
import { TemplatesRoutePage } from "./-orders-pages";

/** The quote templates (orders design O23), orders:settings' as the sidebar offers them. */
export const Route = createFileRoute("/orders/templates")({
  component: TemplatesRoutePage,
});
