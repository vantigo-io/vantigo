import { useQuery } from "@tanstack/react-query";
import { CustomersPage } from "@vantigo/customers-ui/pages/customers.index";
import { fetchSession, sessionQueryKey } from "../../api/auth";
import { getAuthorizationMe } from "../../api/authorization";
import { hasPermissions } from "../../navigation";

/**
 * The customer list, plus the capabilities it needs: `canEdit`
 * (`customers:update`) decides whether Manage tags appears beside the Tag
 * filter (owner and tags design D3). `canExport` (`customers:view`) puts
 * **Export** in the header and `canImport` (`customers:create`,
 * `customers:update` and `customers:view` together — the import operation's
 * own rule) puts **Import** there (customers import/export design D4). The Owner filter needs nothing from the
 * host — "Mine" is the API's own `me`, resolved from the request principal
 * server-side, so the host never has to tell the page who the caller is.
 *
 * Lives beside the route file rather than inside it, the same reason
 * `-customer-overview-tab.tsx` does: the route file may export nothing but its
 * `Route` without costing the bundle a code split.
 */
// All three, each on its own: `hasPermissions` is satisfied by any one of the
// keys it is given, and the import operation wants every one of them.
const IMPORT_PERMISSIONS = ["customers:create", "customers:update", "customers:view"] as const;

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
    />
  );
};
