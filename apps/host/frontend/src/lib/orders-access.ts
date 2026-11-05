import { useQuery } from "@tanstack/react-query";
import type { OrdersAccess } from "@vantigo/orders-ui";
import { fetchSession, sessionQueryKey } from "../api/auth";
import { getAuthorizationMe } from "../api/authorization";
import { hasAllPermissions } from "../navigation";
import { enabledModuleKeys } from "./enabled-modules";

/**
 * What the Orders pages may offer (orders design §8), judged from the
 * caller's permissions and the modules mounted. Every Orders call asks for
 * orders:access as well as its own key, so each capability needs both. The
 * pickers read other modules over HTTP, so each needs that module mounted and
 * its own key: the customer picker `customers:view`, a customer's contacts
 * `customers:contacts-view` and `customers:associations-view`, the variant
 * search `products:products-view`, the free-text line's VAT codes
 * `invoices:access`, the project picker `projects:access`, and the send
 * channel's choices `communications:channels-manage`.
 */
export const ordersAccessOf = (
  permissions: string[] | undefined,
  modules: readonly string[],
  user?: { id?: string; displayName?: string },
): OrdersAccess => {
  const orders = (key: string) => hasAllPermissions(permissions, ["orders:access", key]);
  const mounted = (module: string, keys: readonly string[]) =>
    modules.includes(module) && hasAllPermissions(permissions, keys);
  return {
    canManageQuotes: orders("orders:quotes-manage"),
    canManageOrders: orders("orders:sales-manage"),
    canConfirmOrders: orders("orders:sales-confirm"),
    canManageSettings: orders("orders:settings"),
    canViewAnalysis: orders("orders:analysis-view"),
    canViewCustomers: mounted("customers", ["customers:view"]),
    canViewContacts: mounted("customers", ["customers:contacts-view", "customers:associations-view"]),
    canReadVatCodes: mounted("invoices", ["invoices:access"]),
    canViewProjects: mounted("projects", ["projects:access"]),
    userId: user?.id,
    userDisplayName: user?.displayName,
  };
};

/**
 * The Orders pages' access from the root layout's session and authorization,
 * read from its cache (the same keys), and whether it has answered yet.
 */
export const useOrdersAccess = (): { ready: boolean; access: OrdersAccess } => {
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
    access: ordersAccessOf(authorization.data?.permissions, enabledModuleKeys(), session.data?.user),
  };
};
