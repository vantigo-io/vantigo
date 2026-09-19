import { createFileRoute } from "@tanstack/react-router";
import { SettingsPage } from "@vantigo/time-ui/pages/settings";

export const Route = createFileRoute("/time/settings")({
  component: SettingsPage,
});
