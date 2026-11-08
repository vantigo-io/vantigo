import { createFileRoute } from "@tanstack/react-router";
import { AgentsRoutePage } from "./-agents-pages";

/** The virtual employees (virtual employees design V10). */
export const Route = createFileRoute("/agents/")({
  component: AgentsRoutePage,
});
