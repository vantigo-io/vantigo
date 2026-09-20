import { useParams } from "@tanstack/react-router";
import { ClaimPage } from "../pages/claim";

/**
 * The claim page as a route component, the way the host mounts it: the route
 * owns the path parameter and hands the id over as a number, so nothing in
 * `ClaimPage` reads the router's params itself.
 */
export const ClaimRoute = () => {
  const { claimId } = useParams({ strict: false }) as { claimId: string };
  return <ClaimPage claimId={Number(claimId)} />;
};
