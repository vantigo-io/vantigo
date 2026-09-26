import { createFileRoute } from "@tanstack/react-router";
import { SettingsPage } from "@vantigo/invoices-ui/pages/settings";

export const Route = createFileRoute("/invoices/settings")({
  component: SettingsPage,
});
