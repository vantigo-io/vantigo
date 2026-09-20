import { createFileRoute, notFound } from "@tanstack/react-router";
import { ClaimPage } from "@vantigo/expenses-ui/pages/claim";

/**
 * One travel claim. The path is the package's own `CLAIM_ROUTE_PATH` — "My
 * expenses" and the create form both navigate to it — so it is not a free
 * choice here.
 *
 * The route owns the parameter and hands the id over as a number; no page in
 * the package reads the router's params. A path that is not a positive whole
 * number never reaches the API: it is a not-found, which the `/expenses`
 * layout's own boundary renders, rather than a `GET /claims/NaN` the server
 * would refuse.
 *
 * The module gate and the permission gate are the layout's, as they are for
 * every other expenses route.
 */
export const Route = createFileRoute("/expenses/claims/$claimId")({
  params: {
    parse: ({ claimId }) => ({ claimId: Number(claimId) }),
    stringify: ({ claimId }) => ({ claimId: String(claimId) }),
  },
  beforeLoad: ({ params }) => {
    if (!Number.isInteger(params.claimId) || params.claimId <= 0) throw notFound();
  },
  component: ClaimRoute,
});

function ClaimRoute() {
  const { claimId } = Route.useParams();
  return <ClaimPage claimId={claimId} />;
}
