import { createFileRoute, notFound } from "@tanstack/react-router";
import { SitePage } from "@vantigo/inventory-ui/pages/site";
import { isPositiveId } from "./-ids";

/** One site and its location tree. A path that is not a positive whole number is the layout's not-found. */
export const Route = createFileRoute("/inventory/sites/$siteId")({
  beforeLoad: ({ params }) => {
    if (!isPositiveId(params.siteId)) throw notFound();
  },
  component: SitePage,
});
