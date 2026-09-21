import { useQuery } from "@tanstack/react-query";
import { useParams } from "@tanstack/react-router";
import { CustomerOverview } from "@vantigo/customers-ui/pages/customers.$customerId";
import { fetchSession, sessionQueryKey } from "../../api/auth";
import { getAuthorizationMe } from "../../api/authorization";
import { hasPermissions } from "../../navigation";

/**
 * The customer page's Overview tab: the package's own contact/addresses,
 * contacts and timeline cards, plus `canEdit` (design D6), which this route
 * computes from the caller's `customers:update` permission the same way
 * `-customer-detail-layout.tsx` computes `canArchive`/`canRestore` and
 * `-customer-projects-tab.tsx` computes `canCreate` — the host reads
 * permissions, the package never fetches them itself.
 *
 * Lives beside the route file rather than inside it, the same reason
 * `-customer-projects-tab.tsx` does: the route file may export nothing but
 * its `Route` without costing the bundle a code split.
 */
export const CustomerOverviewTab = () => {
  const { customerId } = useParams({ from: "/customers/$customerId" });
  // The same keys the root layout uses, so this reads its cache.
  const session = useQuery({ queryKey: sessionQueryKey, queryFn: fetchSession, staleTime: 300_000 });
  const authorization = useQuery({
    queryKey: ["authorization", "me", "none"],
    queryFn: getAuthorizationMe,
    enabled: !!session.data,
    retry: false,
    staleTime: 300_000,
  });
  return (
    <CustomerOverview
      customerId={customerId}
      canEdit={hasPermissions(authorization.data?.permissions, ["customers:update"])}
    />
  );
};
