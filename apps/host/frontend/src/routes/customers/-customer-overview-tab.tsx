import { Stack } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import { useParams } from "@tanstack/react-router";
import { CustomerOverview } from "@vantigo/customers-ui/pages/customers.$customerId";
import { fetchSession, sessionQueryKey } from "../../api/auth";
import { getAuthorizationMe } from "../../api/authorization";
import { hasPermissions } from "../../navigation";
import { Customer360Panel } from "./-customer-360-panel";

/**
 * The customer page's Overview tab: the package's own contact/addresses,
 * billing, registry, contacts and timeline cards, plus `canEdit` and
 * `canManageBilling` (design D6) and the two legal-identity capabilities the
 * Registry card sits behind (Brreg in full design D5) — `canViewIdentity`
 * decides whether the card and the registry's offered addresses appear at all,
 * `canManageIdentity` whether Refresh and "Update legal name" do. This route
 * computes all four from the caller's permissions the same way
 * `-customer-detail-layout.tsx` computes `canArchive`/`canRestore` and
 * `-customer-projects-tab.tsx` computes `canCreate` — the host reads
 * permissions, the package never fetches them itself.
 *
 * Lives beside the route file rather than inside it, the same reason
 * `-customer-projects-tab.tsx` does: the route file may export nothing but
 * its `Route` without costing the bundle a code split.
 *
 * `canManageTimeline` (`customers:timeline-manage`, follow-ups design D3) is the
 * newest of them and the one that closes a gap rather than opening one: the
 * timeline card's Add, Edit and Delete controls were server-enforced only, so a
 * reader saw buttons that answered 403.
 *
 * The **Customer 360** panel (customer 360 design D3) sits above the package's
 * cards and is the host's own: it links across modules, and it fetches the
 * overview itself, so it needs nothing from this route but the customer id — no
 * capability prop, because the server already shaped the response to the caller.
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
    <Stack gap="lg">
      <Customer360Panel customerId={customerId} />
      <CustomerOverview
        customerId={customerId}
        canEdit={hasPermissions(authorization.data?.permissions, ["customers:update"])}
        canManageBilling={hasPermissions(authorization.data?.permissions, ["customers:billing-manage"])}
        canViewIdentity={hasPermissions(authorization.data?.permissions, ["customers:legal-identity-view"])}
        canManageIdentity={hasPermissions(authorization.data?.permissions, ["customers:legal-identity-manage"])}
        canManageTimeline={hasPermissions(authorization.data?.permissions, ["customers:timeline-manage"])}
      />
    </Stack>
  );
};
