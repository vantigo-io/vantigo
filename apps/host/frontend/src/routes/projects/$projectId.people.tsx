import { createFileRoute, useParams } from "@tanstack/react-router";
import { ProjectPeople } from "@vantigo/projects-ui/pages/project-people";

const ProjectPeopleRoute = () => {
  const { projectId } = useParams({ from: "/projects/$projectId" });
  return <ProjectPeople projectId={projectId} />;
};

export const Route = createFileRoute("/projects/$projectId/people")({
  component: ProjectPeopleRoute,
});
