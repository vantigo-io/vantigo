import { createFileRoute } from "@tanstack/react-router";
import { validateBoardSearch } from "@vantigo/planning-ui/lib/search";
import { BoardPage } from "@vantigo/planning-ui/pages/board";
import { requirePlanningView } from "./-view-gate";

/**
 * The board (planning design S10), the app's home for `planning:view`. A
 * holder of `planning:access` alone is sent to My planning instead. The
 * search carries every control, and the spotlight's New allocation lands
 * here with `create=allocation`.
 */
export const Route = createFileRoute("/planning/")({
  validateSearch: validateBoardSearch,
  beforeLoad: requirePlanningView,
  component: BoardPage,
});
