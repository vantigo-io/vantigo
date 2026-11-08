import { createFileRoute, notFound } from "@tanstack/react-router";
import { RunRoutePage } from "./-agents-pages";
import { isUuid } from "./-ids";

/** One run: its timeline, verdict and audit lines, agents:activity's. A path that is not a uuid is the layout's not-found. */
export const Route = createFileRoute("/agents/runs/$runId")({
  beforeLoad: ({ params }) => {
    if (!isUuid(params.runId)) throw notFound();
  },
  component: RunRoutePage,
});
