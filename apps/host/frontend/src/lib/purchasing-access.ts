import { useQuery } from "@tanstack/react-query";
import { PURCHASE_ORDERS_ROUTE_PATH, type PurchasingAccess, RECEIVING_ROUTE_PATH } from "@vantigo/purchasing-ui";
import { fetchSession, sessionQueryKey } from "../api/auth";
import { getAuthorizationMe } from "../api/authorization";
import { hasAllPermissions } from "../navigation";
import { enabledModuleKeys } from "./enabled-modules";

/**
 * What the Purchasing pages may offer (purchasing design §8), judged from the
 * caller's permissions. Every Purchasing call but receiving's asks for
 * purchasing:access as well as its own key, so each of those capabilities
 * needs both. Receiving — the receiving list, the receive screen, a receipt,
 * its undo and the delivered mark — asks for purchasing:receive alone and
 * answers no price (P30, A47), so canReceive is that key alone. Whether the
 * caller may write a supplier in the register is Purchasing's meta's (A12),
 * not this.
 */
export const purchasingAccessOf = (permissions: string[] | undefined, user?: { id?: string }): PurchasingAccess => {
  const purchasing = (key: string) => hasAllPermissions(permissions, ["purchasing:access", key]);
  return {
    canManageOrders: purchasing("purchasing:orders-manage"),
    canSendOrders: purchasing("purchasing:orders-send"),
    canManageSupplierItems: purchasing("purchasing:supplier-items-manage"),
    canManageSettings: purchasing("purchasing:settings"),
    canReceive: hasAllPermissions(permissions, ["purchasing:receive"]),
    canRead: hasAllPermissions(permissions, ["purchasing:access"]),
    canMatchInvoices: purchasing("purchasing:invoices-match"),
    // Register invoice makes Accounting's draft, so it needs Accounting's
    // own key as well (A1).
    canRegisterInvoices: hasAllPermissions(permissions, [
      "purchasing:access",
      "purchasing:invoices-match",
      "accounting:post",
    ]),
    canOpenInventoryMovements:
      enabledModuleKeys().includes("inventory") && hasAllPermissions(permissions, ["inventory:stock-view"]),
    userId: user?.id,
  };
};

/**
 * Where the app's prefix sends a caller: the purchase orders, its home (P23),
 * or the orders to receive for a receiver who may not read orders (A47).
 */
export const purchasingHomeFor = (access: PurchasingAccess): string =>
  !access.canRead && access.canReceive ? RECEIVING_ROUTE_PATH : PURCHASE_ORDERS_ROUTE_PATH;

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

/**
 * Whether **Order…** is offered on Inventory's low-stock row and on
 * Manufacturing's buy suggestion (purchasing design P35): Purchasing is
 * enabled and the caller may make orders from proposals — purchasing:access
 * with purchasing:orders-manage.
 */
export const canOrderFromProposals = (permissions: string[] | undefined, enabled = enabledModuleKeys()): boolean =>
  enabled.includes("purchasing") && purchasingAccessOf(permissions).canManageOrders;

/** {@link canOrderFromProposals} for the signed-in caller, false until the authorization has answered. */
export const useCanOrderFromProposals = (): boolean => canOrderFromProposals(usePurchasingAccess().permissions);
