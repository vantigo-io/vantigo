import { createFileRoute } from "@tanstack/react-router";
import { UnbookedPage } from "@vantigo/planning-ui/pages/unbooked";

export const Route = createFileRoute("/planning/unbooked")({
  component: UnbookedPage,
});
