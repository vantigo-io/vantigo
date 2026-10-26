import { createFileRoute } from "@tanstack/react-router";
import { MyPlanningPage } from "@vantigo/planning-ui/pages/me";

export const Route = createFileRoute("/planning/me")({
  component: MyPlanningPage,
});
