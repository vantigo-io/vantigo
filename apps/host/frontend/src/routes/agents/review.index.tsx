import { createFileRoute } from "@tanstack/react-router";
import { ReviewRoutePage } from "./-agents-pages";

/** The review queue across agents, agents:review's. */
export const Route = createFileRoute("/agents/review/")({
  component: ReviewRoutePage,
});
