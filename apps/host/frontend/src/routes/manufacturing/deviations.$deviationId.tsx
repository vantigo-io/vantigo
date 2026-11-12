import { createFileRoute, notFound } from "@tanstack/react-router";
import { DeviationPage } from "@vantigo/manufacturing-ui/pages/deviation";
import { isPositiveId } from "./-ids";

/**
 * One deviation (manufacturing phase 2 M99): the lots it holds, its close and
 * the release of its holds. A path that is not a positive whole number is
 * the layout's not-found.
 */
export const Route = createFileRoute("/manufacturing/deviations/$deviationId")({
  beforeLoad: ({ params }) => {
    if (!isPositiveId(params.deviationId)) throw notFound();
  },
  component: DeviationPage,
});
