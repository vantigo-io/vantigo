import { createFileRoute, useParams } from "@tanstack/react-router";
import { ProjectOverview } from "@vantigo/projects-ui/pages/projects.$projectId";

const ProjectOverviewRoute = () => {
  const { projectId } = useParams({ from: "/projects/$projectId" });
  return <ProjectOverview projectId={projectId} />;
};

export const Route = createFileRoute("/projects/$projectId/")({
  component: ProjectOverviewRoute,
});
