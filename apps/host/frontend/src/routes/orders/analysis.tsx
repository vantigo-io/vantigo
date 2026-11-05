import { createFileRoute } from "@tanstack/react-router";
import { AnalysisRoutePage } from "./-orders-pages";

/** Orders' analysis of quotes and orders (orders design O35), orders:analysis-view's as the sidebar offers it. */
export const Route = createFileRoute("/orders/analysis")({
  component: AnalysisRoutePage,
});
