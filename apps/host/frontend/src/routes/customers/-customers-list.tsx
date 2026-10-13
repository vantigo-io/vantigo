import { useQuery } from "@tanstack/react-query";
import { CustomersPage } from "@vantigo/customers-ui/pages/customers.index";
import { fetchSession, sessionQueryKey } from "../../api/auth";
import { getAuthorizationMe } from "../../api/authorization";
import { hasPermissions } from "../../navigation";

// Both, each on its own: `hasPermissions` is satisfied by any one of the
// keys it is given, and the import's operations want both (customers import
// wizard design D4). What the mapping may write is the import page's own
// question, asked of the other keys there.
export const IMPORT_PERMISSIONS = ["customers:import", "customers:view"] as const;

/**
 * The customer list, plus the capabilities it needs: `canEdit`
 * (`customers:update`) decides whether Manage tags appears beside the Tag
 * filter (owner and tags design D3). `canExport` (`customers:view`) puts
 * **Export** in the header and `canImport` (`customers:import` and
 * `customers:view` together — the import's own rule) puts **Import** there, a
 * link to the import page (customers import wizard design D8).
 * `canMerge` (`customers:merge`) lets the create and edit forms'
 * duplicate-identity conflict suggest a merge (customers merge design D4). The
 * Owner filter needs nothing from the host — "Mine" is the API's own `me`,
 * resolved from the request principal server-side, so the host never has to
 * tell the page who the caller is.
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
  const permissions = authorization.data?.permissions;
  return (
    <CustomersPage
      canEdit={hasPermissions(permissions, ["customers:update"])}
      canExport={hasPermissions(permissions, ["customers:view"])}
      canImport={IMPORT_PERMISSIONS.every((permission) => hasPermissions(permissions, [permission]))}
      canMerge={hasPermissions(permissions, ["customers:merge"])}
    />
  );
};
