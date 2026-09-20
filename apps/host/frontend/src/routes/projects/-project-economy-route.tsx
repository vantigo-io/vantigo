import { useParams } from "@tanstack/react-router";
import { ProjectEconomy } from "@vantigo/projects-ui/pages/project-economy";
import { enabledModuleKeys } from "../../lib/enabled-modules";

/**
 * The Economy tab. It is shown to everyone who sees the project; the page
 * itself shapes its content per caller (hours only, budget amounts, cost and
 * margin) from what the economy endpoint returns, and refuses an outsider with
 * its own locked state.
 *
 * `expensesHref` is the one thing the projects package cannot know: where this
 * project's expenses live in the host's routes. It is handed over only when
 * the installation has mounted Expenses — without it the tab does not exist
 * and the link would lead to a "not enabled" page — and the row it belongs to
 * is already hidden unless the server reports lines ready to invoice.
 */
export const ProjectEconomyRoute = () => {
  const { projectId } = useParams({ from: "/projects/$projectId" });
  const expenses = enabledModuleKeys().includes("expenses");
  return (
    <ProjectEconomy projectId={projectId} expensesHref={expenses ? `/projects/${projectId}/expenses` : undefined} />
  );
};
