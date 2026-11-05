import { createFileRoute } from "@tanstack/react-router";
import { SettingsRoutePage } from "./-orders-pages";

/** Orders' settings (orders design §9), orders:settings' as the sidebar offers them. */
export const Route = createFileRoute("/orders/settings")({
  component: SettingsRoutePage,
});
