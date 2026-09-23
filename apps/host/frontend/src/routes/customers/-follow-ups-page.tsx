import { useQuery } from "@tanstack/react-query";
import { FollowUpsPage } from "@vantigo/customers-ui/pages/follow-ups";
import { fetchSession, sessionQueryKey } from "../../api/auth";
import { getAuthorizationMe } from "../../api/authorization";
import { hasPermissions } from "../../navigation";

/**
 * The Follow-ups page with its one capability: `canManageTimeline`
 * (`customers:timeline-manage`, follow-ups design D3), which decides whether
 * each row carries a Done tick. Computed here, from the caller's permissions,
 * the same way `-customer-overview-tab.tsx` computes its four — the host reads
 * permissions, the package never fetches them itself.
 *
 * Lives beside the route file rather than inside it for the same reason that
 * one does: the route file may export nothing but its `Route` without costing
 * the bundle a code split.
 */
export const FollowUpsTab = () => {
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
    <FollowUpsPage canManageTimeline={hasPermissions(authorization.data?.permissions, ["customers:timeline-manage"])} />
  );
};
