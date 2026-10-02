import { useQuery } from "@tanstack/react-query";
import { fetchSession, sessionQueryKey } from "../api/auth";
import { getAuthorizationMe } from "../api/authorization";
import { hasPermissions } from "../navigation";

/**
 * What the Invoices pages need from the host's session: whether the caller
 * may search customers — the buyer picker reads the customers module's list,
 * which needs `customers:view` (invoices foundation design D1) — whether it
 * may make a draft (`invoices:create`, the customer page's Invoices tab), and
 * the name a new draft's "Vår ref." is prefilled with. Both queries share the
 * root layout's keys, so they read its cache rather than refetching.
 */
export const useInvoiceAccess = () => {
  const session = useQuery({ queryKey: sessionQueryKey, queryFn: fetchSession, staleTime: 300_000 });
  const authorization = useQuery({
    queryKey: ["authorization", "me", "none"],
    queryFn: getAuthorizationMe,
    enabled: !!session.data,
    retry: false,
    staleTime: 300_000,
  });
  return {
    ready: authorization.isSuccess || authorization.isError,
    canViewCustomers: hasPermissions(authorization.data?.permissions, ["customers:view"]),
    canCreateInvoices: hasPermissions(authorization.data?.permissions, ["invoices:create"]),
    userDisplayName: session.data?.user.displayName,
  };
};
