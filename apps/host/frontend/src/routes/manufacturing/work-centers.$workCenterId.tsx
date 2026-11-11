import { createFileRoute, notFound } from "@tanstack/react-router";
import { WorkCenterPage } from "@vantigo/manufacturing-ui/pages/work-center";
import { isPositiveId } from "./-ids";

/** One work center: its form and its closures. A path that is not a positive whole number is the layout's not-found. */
export const Route = createFileRoute("/manufacturing/work-centers/$workCenterId")({
  beforeLoad: ({ params }) => {
    if (!isPositiveId(params.workCenterId)) throw notFound();
  },
  component: WorkCenterPage,
});
