import { createFileRoute } from "@tanstack/react-router";
import { validateRequestsSearch } from "@vantigo/planning-ui/lib/search";
import { RequestsPage } from "@vantigo/planning-ui/pages/requests";
import { requirePlanningView } from "./-view-gate";

/**
 * The requests (planning design S16), open, filled and withdrawn,
 * `planning:view`'s. The search carries the filters, and the spotlight's New
 * request lands here with `new=1`. A caller without `planning:view` is sent
 * to My planning, as from the board.
 */
export const Route = createFileRoute("/planning/requests")({
  validateSearch: validateRequestsSearch,
  beforeLoad: requirePlanningView,
  component: RequestsPage,
});
