import { useQuery } from "@tanstack/react-query";
import { useParams } from "@tanstack/react-router";
import { customerQueryOptions } from "@vantigo/customers-ui/api/customers";
import { isReadOnlyCustomer } from "@vantigo/customers-ui/lib/customer-read-only";
import { useI18n } from "@vantigo/frontend-shell";
import { CustomerOrdersPanel } from "@vantigo/orders-ui";
import { fetchSession, sessionQueryKey } from "../../api/auth";
import { getAuthorizationMe } from "../../api/authorization";
import { ModuleNotEnabledPage } from "../../components/errors";
import { enabledModuleKeys } from "../../lib/enabled-modules";
import { hasPermissions } from "../../navigation";
import "../../i18n";

/**
 * The customer page's Orders tab (orders design O31a). It lives in the
 * customers app but calls the orders API, so it gates on the orders module
 * itself, as the Invoices tab does: a pasted link must not hit an API 404. The
 * panel is the package's; whether this caller may start a quote or an order
 * from it is the host's answer: `orders:quotes-manage` for *New quote*,
 * `orders:sales-manage` for *New order*, and only on a customer that takes
 * changes — neither merged away nor anonymised (`isReadOnlyCustomer`).
 *
 * It sits beside the route file rather than inside it because the route file
 * may export nothing but its `Route` without costing the bundle a code split.
 */
export const CustomerOrdersTab = () => {
  const { t } = useI18n("host");
  const { customerId } = useParams({ from: "/customers/$customerId" });
  // The same keys the root layout uses, so this reads its cache.
  const session = useQuery({ queryKey: sessionQueryKey, queryFn: fetchSession, staleTime: 300_000 });
  const authorization = useQuery({
    queryKey: ["authorization", "me", "none"],
    queryFn: getAuthorizationMe,
    enabled: !!session.data,
    retry: false,
    staleTime: 300_000,
  });
  // The layout's loader has already put the customer in the cache.
  const customer = useQuery(customerQueryOptions(customerId));
  if (!enabledModuleKeys().includes("orders")) return <ModuleNotEnabledPage appLabel={t("navigation.orders")} />;
  const permissions = authorization.data?.permissions;
  const writable = customer.data !== undefined && !isReadOnlyCustomer(customer.data);
  return (
    <CustomerOrdersPanel
      customerId={customerId}
      canCreateQuote={writable && hasPermissions(permissions, ["orders:quotes-manage"])}
      canCreateOrder={writable && hasPermissions(permissions, ["orders:sales-manage"])}
    />
  );
};
