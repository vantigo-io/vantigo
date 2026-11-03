import { createFileRoute, notFound } from "@tanstack/react-router";
import { MovementPage } from "@vantigo/inventory-ui/pages/movement";
import { isPositiveId } from "./-ids";

/** One movement. A path that is not a positive whole number is the layout's not-found. */
export const Route = createFileRoute("/inventory/movements/$movementId")({
  beforeLoad: ({ params }) => {
    if (!isPositiveId(params.movementId)) throw notFound();
  },
  component: MovementPage,
});
