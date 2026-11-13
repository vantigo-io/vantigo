import { createFileRoute, notFound } from "@tanstack/react-router";
import { UnbuildPage } from "@vantigo/manufacturing-ui/pages/unbuild";
import { isPositiveId } from "./-ids";

/**
 * One unbuild (manufacturing phase 2 M123): what it took apart and restored,
 * and the release of its quarantine; Inventory's movement page, trace and lot
 * page link here. A path that is not a positive whole number is the layout's
 * not-found.
 */
export const Route = createFileRoute("/manufacturing/unbuilds/$unbuildId")({
  beforeLoad: ({ params }) => {
    if (!isPositiveId(params.unbuildId)) throw notFound();
  },
  component: UnbuildPage,
});
