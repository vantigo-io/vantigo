import { createFileRoute, useParams } from "@tanstack/react-router";
import { ProjectBilling } from "@vantigo/projects-ui/pages/project-billing";

// The tab is hidden without the project's canSeeFinancials capability, but the
// URL can still be pasted: the page asks the project first and renders its own
// forbidden state, so a deep link is refused rather than broken.
const ProjectBillingRoute = () => {
  const { projectId } = useParams({ from: "/projects/$projectId" });
  return <ProjectBilling projectId={projectId} />;
};

export const Route = createFileRoute("/projects/$projectId/billing")({
  component: ProjectBillingRoute,
});
