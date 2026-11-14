import { createFileRoute } from "@tanstack/react-router";
import { ReportsPage } from "@vantigo/accounting-ui/pages/reports";

/** The specifications (accounting design A11). */
export const Route = createFileRoute("/accounting/reports")({
  component: ReportsPage,
});
