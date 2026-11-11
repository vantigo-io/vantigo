import { createFileRoute, notFound } from "@tanstack/react-router";
import { BomPage } from "@vantigo/manufacturing-ui/pages/bom";
import { isPositiveId } from "./-ids";

/** One bill of materials' editor. A path that is not a positive whole number is the layout's not-found. */
export const Route = createFileRoute("/manufacturing/boms/$bomId/")({
  beforeLoad: ({ params }) => {
    if (!isPositiveId(params.bomId)) throw notFound();
  },
  component: BomPage,
});
