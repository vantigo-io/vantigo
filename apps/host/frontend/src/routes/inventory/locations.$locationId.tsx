import { createFileRoute, notFound } from "@tanstack/react-router";
import { LocationPage } from "@vantigo/inventory-ui/pages/location";
import { isPositiveId } from "./-ids";

/** One location and what lies on it (inventory design I29). A path that is not a positive whole number is the layout's not-found. */
export const Route = createFileRoute("/inventory/locations/$locationId")({
  beforeLoad: ({ params }) => {
    if (!isPositiveId(params.locationId)) throw notFound();
  },
  component: LocationPage,
});
