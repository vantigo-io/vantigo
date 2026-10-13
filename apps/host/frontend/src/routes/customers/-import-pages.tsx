import { useQuery } from "@tanstack/react-query";
import { useParams } from "@tanstack/react-router";
import { ImportPage } from "@vantigo/customers-ui/pages/import.$importId";
import { ImportStartPage } from "@vantigo/customers-ui/pages/import.index";
import { fetchSession, sessionQueryKey } from "../../api/auth";
import { getAuthorizationMe } from "../../api/authorization";
import { hasPermissions } from "../../navigation";

/**
 * The import pages (customers import wizard design D8). The start page needs
 * nothing from the host but the route; the import page takes the four
 * capabilities its mapping step shows and hides by (design D4): the modes
 * `customers:create` and `customers:update` allow, and the legal-identity
 * and billing groups `customers:legal-identity-manage` and
 * `customers:billing-manage` allow. Computed here from the caller's
 * permissions, the way `-customer-overview-tab.tsx` computes its own — the
 * host reads permissions, the package never fetches them itself. The door,
 * `customers:import` with `customers:view`, is the list page's `canImport`
 * and the server's rule on every operation; a caller without it reaches
 * these pages only by typing the address, and is answered 403 by the first
 * request.
 *
 * Lives beside the route files rather than inside them, the same reason
 * `-customers-list.tsx` does: a route file may export nothing but its
 * `Route` without costing the bundle a code split.
 */
const useCapabilities = () => {
  // The same keys the root layout uses, so this reads its cache.
  const session = useQuery({ queryKey: sessionQueryKey, queryFn: fetchSession, staleTime: 300_000 });
  const authorization = useQuery({
    queryKey: ["authorization", "me", "none"],
    queryFn: getAuthorizationMe,
    enabled: !!session.data,
    retry: false,
    staleTime: 300_000,
  });
  const permissions = authorization.data?.permissions;
  return {
    canCreate: hasPermissions(permissions, ["customers:create"]),
    canUpdate: hasPermissions(permissions, ["customers:update"]),
    canMapIdentity: hasPermissions(permissions, ["customers:legal-identity-manage"]),
    canMapBilling: hasPermissions(permissions, ["customers:billing-manage"]),
  };
};

export const ImportStartRoutePage = () => <ImportStartPage />;

export const ImportRoutePage = () => {
  const { importId } = useParams({ from: "/customers/import/$importId" });
  const capabilities = useCapabilities();
  return <ImportPage importId={importId} capabilities={capabilities} />;
};
