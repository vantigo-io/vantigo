import { createFileRoute, notFound } from "@tanstack/react-router";
import { ShopFloorWorkCenterPage } from "@vantigo/manufacturing-ui/pages/shop-floor";
import { isPositiveId } from "./-ids";

/**
 * A work center on the shop floor: its queue and Block / Unblock
 * (manufacturing design M30, M31). A path that is not a positive whole number
 * is the layout's not-found.
 */
export const Route = createFileRoute("/manufacturing/shop-floor/work-centers/$workCenterId")({
  beforeLoad: ({ params }) => {
    if (!isPositiveId(params.workCenterId)) throw notFound();
  },
  component: ShopFloorWorkCenterPage,
});
