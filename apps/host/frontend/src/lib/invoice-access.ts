import { useQuery } from "@tanstack/react-query";
import { invoiceHref } from "@vantigo/invoices-ui/lib/routes";
import { fetchSession, sessionQueryKey } from "../api/auth";
import { getAuthorizationMe } from "../api/authorization";
import { hasPermissions } from "../navigation";
import { enabledModuleKeys } from "./enabled-modules";

/**
 * What the Invoices pages need from the host's session: whether the caller
 * may search customers — the buyer picker reads the customers module's list,
 * which needs `customers:view` (invoices foundation design D1) — whether it
 * may make a draft (`invoices:create`, the customer page's Invoices tab), the
 * name a new draft's "Vår ref." is prefilled with, and the caller's id, so the
 * Payments area says "you" of an import or an event of theirs. Both queries share the
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
    // The API asks both together; `hasPermissions` is any-of, so two calls.
    canCreateInvoices:
      hasPermissions(authorization.data?.permissions, ["invoices:access"]) &&
      hasPermissions(authorization.data?.permissions, ["invoices:create"]),
    userDisplayName: session.data?.user.displayName,
    userId: session.data?.user.id,
  };
};

/**
 * Where an invoice lives in the host's routes, for the source apps' "Invoiced
 * by invoice n" (invoices work design D18): handed over exactly when the
 * caller can open it — the invoices module mounted and `invoices:access`, the
 * permission every read of that API demands. Otherwise undefined, and the
 * badge is plain words: a link that lands on a refusal says nothing a badge
 * does not.
 */
export const useInvoiceHref = (): ((invoiceId: number) => string) | undefined => {
  const session = useQuery({ queryKey: sessionQueryKey, queryFn: fetchSession, staleTime: 300_000 });
  const authorization = useQuery({
    queryKey: ["authorization", "me", "none"],
    queryFn: getAuthorizationMe,
    enabled: !!session.data,
    retry: false,
    staleTime: 300_000,
  });
  const open =
    enabledModuleKeys().includes("invoices") && hasPermissions(authorization.data?.permissions, ["invoices:access"]);
  return open ? invoiceHref : undefined;
};
