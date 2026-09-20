import { createFileRoute } from "@tanstack/react-router";
import { ProjectEconomyRoute } from "./-project-economy-route";

export const Route = createFileRoute("/projects/$projectId/economy")({
  component: ProjectEconomyRoute,
});
