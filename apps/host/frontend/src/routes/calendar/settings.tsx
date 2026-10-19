import { createFileRoute } from "@tanstack/react-router";
import { SettingsPage } from "@vantigo/calendar-ui";

export const Route = createFileRoute("/calendar/settings")({
  component: SettingsPage,
});
