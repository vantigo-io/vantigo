import { createFileRoute } from "@tanstack/react-router";
import { SettingsRoutePage } from "./-agents-pages";

/** The company name and the retention, agents:manage's. */
export const Route = createFileRoute("/agents/settings")({
  component: SettingsRoutePage,
});
