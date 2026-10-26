import { createFileRoute } from "@tanstack/react-router";
import { IntakePage } from "@vantigo/planning-ui/pages/intake";
import { requirePlanningView } from "./-view-gate";

/**
 * The intake (planning design S18): the planned projects with each request's
 * coverage, `planning:view`'s. A caller without `planning:view` is sent to My
 * planning, as from the board.
 */
export const Route = createFileRoute("/planning/intake")({
  beforeLoad: requirePlanningView,
  component: IntakePage,
});
