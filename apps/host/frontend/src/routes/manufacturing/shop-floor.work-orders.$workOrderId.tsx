import { createFileRoute, notFound } from "@tanstack/react-router";
import { WorkOrderPage } from "@vantigo/manufacturing-ui/pages/work-order";
import { isPositiveId } from "./-ids";

/**
 * One work order on the shop floor: Start, Pause and Finish, its timer,
 * instructions and components (manufacturing design M31). A path that is not
 * a positive whole number is the layout's not-found.
 */
export const Route = createFileRoute("/manufacturing/shop-floor/work-orders/$workOrderId")({
  beforeLoad: ({ params }) => {
    if (!isPositiveId(params.workOrderId)) throw notFound();
  },
  component: WorkOrderPage,
});
