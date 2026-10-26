import { useParams } from "@tanstack/react-router";
import { ProjectPlan } from "../components/project-plan";

/**
 * What the host puts on the project page's Planning tab: a route of another
 * app, whose parameter is a string the host turns into the project's id — the
 * component is given the project and reads nothing from the route.
 */
export const ProjectPlanningTab = () => {
  const { projectId } = useParams({ strict: false }) as { projectId: string };
  return <ProjectPlan projectId={Number(projectId)} />;
};
