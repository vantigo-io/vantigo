import { createFileRoute } from "@tanstack/react-router";
import { ReportsPage } from "@vantigo/manufacturing-ui/pages/reports";

/** The reports, under the overview (manufacturing design M42). */
export const Route = createFileRoute("/manufacturing/reports")({
  component: ReportsPage,
});
