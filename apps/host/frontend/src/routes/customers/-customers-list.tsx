import { useQuery } from "@tanstack/react-query";
import { CustomersPage } from "@vantigo/customers-ui/pages/customers.index";
import { fetchSession, sessionQueryKey } from "../../api/auth";
import { getAuthorizationMe } from "../../api/authorization";
import { hasPermissions } from "../../navigation";

/**
 * The customer list, plus the one capability it now needs: `canEdit`
 * (`customers:update`) decides whether Manage tags appears beside the Tag
 * filter (owner and tags design D3). The Owner filter needs nothing from the
 * host — "Mine" is the API's own `me`, resolved from the request principal
 * server-side, so the host never has to tell the page who the caller is.
 *
 * Lives beside the route file rather than inside it, the same reason
 * `-customer-overview-tab.tsx` does: the route file may export nothing but its
 * `Route` without costing the bundle a code split.
 */
export const CustomersListPage = () => {
  const session = useQuery({ queryKey: sessionQueryKey, queryFn: fetchSession, staleTime: 300_000 });
  const authorization = useQuery({
    queryKey: ["authorization", "me", "none"],
    queryFn: getAuthorizationMe,
    enabled: !!session.data,
    retry: false,
    staleTime: 300_000,
  });
  return <CustomersPage canEdit={hasPermissions(authorization.data?.permissions, ["customers:update"])} />;
};
