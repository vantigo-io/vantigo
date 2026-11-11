import { createFileRoute } from "@tanstack/react-router";
import { SchedulePage } from "@vantigo/manufacturing-ui/pages/schedule";

/** The production schedule board (manufacturing design M36). */
export const Route = createFileRoute("/manufacturing/schedule")({
  component: SchedulePage,
});
