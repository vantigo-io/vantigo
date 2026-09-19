import { createFileRoute, useParams } from "@tanstack/react-router";
import { ProjectEconomy } from "@vantigo/projects-ui/pages/project-economy";

// The tab is hidden without the project's canSeeFinancials capability, but the
// URL can still be pasted: the page asks the project first and renders its own
// locked state, so a deep link is refused rather than broken.
const ProjectEconomyRoute = () => {
  const { projectId } = useParams({ from: "/projects/$projectId" });
  return <ProjectEconomy projectId={projectId} />;
};

export const Route = createFileRoute("/projects/$projectId/economy")({
  component: ProjectEconomyRoute,
});
