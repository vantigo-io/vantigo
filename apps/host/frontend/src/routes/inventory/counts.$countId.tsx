import { createFileRoute, notFound } from "@tanstack/react-router";
import { CountPage } from "@vantigo/inventory-ui/pages/count";
import { isPositiveId } from "./-ids";

/** One count. A path that is not a positive whole number is the layout's not-found. */
export const Route = createFileRoute("/inventory/counts/$countId")({
  beforeLoad: ({ params }) => {
    if (!isPositiveId(params.countId)) throw notFound();
  },
  component: CountPage,
});
