import { createFileRoute } from "@tanstack/react-router";
import { ProjectTimeTab } from "./-project-time-tab";

export const Route = createFileRoute("/projects/$projectId/time")({
  component: ProjectTimeTab,
});
