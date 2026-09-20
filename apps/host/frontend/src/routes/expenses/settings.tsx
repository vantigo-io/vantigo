import { createFileRoute } from "@tanstack/react-router";
import { SettingsPage } from "@vantigo/expenses-ui/pages/settings";

export const Route = createFileRoute("/expenses/settings")({
  component: SettingsPage,
});
