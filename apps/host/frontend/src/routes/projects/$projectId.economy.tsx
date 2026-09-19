import { createFileRoute, useParams } from "@tanstack/react-router";
import { ProjectEconomy } from "@vantigo/projects-ui/pages/project-economy";

// The tab is shown to everyone who sees the project; the page itself shapes
// its content per caller (hours only, budget amounts, cost and margin) from
// what the economy endpoint returns, and refuses an outsider with its own
// locked state.
const ProjectEconomyRoute = () => {
  const { projectId } = useParams({ from: "/projects/$projectId" });
  return <ProjectEconomy projectId={projectId} />;
};

export const Route = createFileRoute("/projects/$projectId/economy")({
  component: ProjectEconomyRoute,
});
