import { createFileRoute } from "@tanstack/react-router";
import { NewWorkCenterPage } from "@vantigo/manufacturing-ui/pages/work-center";

/** A new work center's form; it says so when the caller may not write one. */
export const Route = createFileRoute("/manufacturing/work-centers/new")({
  component: NewWorkCenterPage,
});
