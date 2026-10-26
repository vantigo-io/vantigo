import { createFileRoute } from "@tanstack/react-router";
import { PlanningSettingsPage } from "@vantigo/planning-ui/pages/settings";

export const Route = createFileRoute("/planning/settings")({
  component: PlanningSettingsPage,
});
