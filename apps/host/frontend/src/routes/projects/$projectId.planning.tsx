import { createFileRoute } from "@tanstack/react-router";
import { ProjectPlanningTab } from "./-project-planning-tab";

export const Route = createFileRoute("/projects/$projectId/planning")({
  component: ProjectPlanningTab,
});
