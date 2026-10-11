import { createFileRoute } from "@tanstack/react-router";
import { SettingsPage } from "@vantigo/staff-ui/pages/settings";

export const Route = createFileRoute("/staff/settings")({
  component: SettingsPage,
});
