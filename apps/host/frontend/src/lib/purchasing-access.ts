import { useQuery } from "@tanstack/react-query";
import type { PurchasingAccess } from "@vantigo/purchasing-ui";
import { fetchSession, sessionQueryKey } from "../api/auth";
import { getAuthorizationMe } from "../api/authorization";
import { hasAllPermissions } from "../navigation";

/**
 * What the Purchasing pages may offer (purchasing design §8), judged from the
 * caller's permissions. Every Purchasing call asks for purchasing:access as
 * well as its own key, so each capability needs both. Whether the caller may
 * write a supplier in the register is Purchasing's meta's (A12), not this.
 */
export const purchasingAccessOf = (permissions: string[] | undefined, user?: { id?: string }): PurchasingAccess => {
  const purchasing = (key: string) => hasAllPermissions(permissions, ["purchasing:access", key]);
  return {
    canManageOrders: purchasing("purchasing:orders-manage"),
    canSendOrders: purchasing("purchasing:orders-send"),
    canManageSupplierItems: purchasing("purchasing:supplier-items-manage"),
    canManageSettings: purchasing("purchasing:settings"),
    userId: user?.id,
  };
};

/**
 * Whether the register's supplier form opened from Purchasing keeps the
 * books' own fields (A12, A26): only for a poster of the books, who holds
 * accounting:post and accounting:read. Anyone else — a buyer with
 * accounting:suppliers-manage alone — gets the form without them, read
 * through Purchasing.
 */
export const supplierFormHasBooks = (permissions: string[] | undefined): boolean =>
  hasAllPermissions(permissions, ["accounting:post", "accounting:read"]);

/**
 * The Purchasing pages' access from the root layout's session and
 * authorization, read from its cache (the same keys), the caller's
 * permissions with it, and whether it has answered yet.
 */
export const usePurchasingAccess = (): { ready: boolean; access: PurchasingAccess; permissions: string[] } => {
  const session = useQuery({ queryKey: sessionQueryKey, queryFn: fetchSession, staleTime: 300_000 });
  const authorization = useQuery({
    queryKey: ["authorization", "me", "none"],
    queryFn: getAuthorizationMe,
    enabled: !!session.data,
    retry: false,
    staleTime: 300_000,
  });
  const permissions = authorization.data?.permissions ?? [];
  return {
    ready: authorization.isSuccess || authorization.isError,
    access: purchasingAccessOf(permissions, session.data?.user),
    permissions,
  };
};
