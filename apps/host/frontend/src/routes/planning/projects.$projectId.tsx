import { createFileRoute, notFound } from "@tanstack/react-router";
import { validateWindowSearch } from "@vantigo/planning-ui/lib/search";
import { ProjectPlanPage } from "@vantigo/planning-ui/pages/project-plan";
import { isPositiveId, requirePlanningView } from "./-view-gate";

/**
 * One project's plan (planning design S11), `planning:view`'s. A path that is
 * not a positive whole number is the layout's not-found; a caller without
 * `planning:view` is sent to My planning, as from the board.
 */
export const Route = createFileRoute("/planning/projects/$projectId")({
  validateSearch: validateWindowSearch,
  beforeLoad: async ({ params, context }) => {
    if (!isPositiveId(params.projectId)) throw notFound();
    await requirePlanningView({ context });
  },
  component: ProjectPlanPage,
});
