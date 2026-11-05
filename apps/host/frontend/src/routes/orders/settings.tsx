import { createFileRoute } from "@tanstack/react-router";
import { SettingsPage } from "@vantigo/orders-ui/pages/settings";

/** Orders' settings (orders design §9), orders:settings' as the sidebar offers them. */
export const Route = createFileRoute("/orders/settings")({
  component: SettingsPage,
});
