import { useQuery } from "@tanstack/react-query";
import type { ProductMessaging } from "@vantigo/communications-ui";
import { newSalesOrderLinkOptions } from "@vantigo/orders-ui";
import { fetchSession, sessionQueryKey } from "../api/auth";
import { getAuthorizationMe } from "../api/authorization";
import { hasAllPermissions } from "../navigation";
import { enabledModuleKeys } from "./enabled-modules";

/**
 * What a Messenger chat may offer of the modules beside Communications (Messenger design N16, N17), judged
 * from the caller's permissions and the modules mounted: *Attach product* needs Products mounted and
 * `products:products-view`, the price in a card's subtitle `products:pricing-view`, and *Create order*
 * Orders mounted and the keys of a new order (`orders:access` and `orders:sales-manage`).
 */
export const productMessagingOf = (permissions: string[] | undefined, modules: readonly string[]): ProductMessaging => {
  const mounted = (module: string, keys: readonly string[]) =>
    modules.includes(module) && hasAllPermissions(permissions, keys);
  const canCreateOrders = mounted("orders", ["orders:access", "orders:sales-manage"]);
  return {
    canAttachProduct: mounted("products", ["products:products-view"]),
    canViewPricing: mounted("products", ["products:products-view", "products:pricing-view"]),
    ...(canCreateOrders
      ? {
          newOrder: ({ customerId, productId }) =>
            newSalesOrderLinkOptions(customerId ?? undefined, { productId }) as {
              to: string;
              search: Record<string, number>;
            },
        }
      : {}),
  };
};

/** The chat's product messaging from the root layout's session and authorization, read from its cache. */
export const useProductMessaging = (): ProductMessaging => {
  const session = useQuery({ queryKey: sessionQueryKey, queryFn: fetchSession, staleTime: 300_000 });
  const authorization = useQuery({
    queryKey: ["authorization", "me", "none"],
    queryFn: getAuthorizationMe,
    enabled: !!session.data,
    retry: false,
    staleTime: 300_000,
  });
  return productMessagingOf(authorization.data?.permissions, enabledModuleKeys());
};
