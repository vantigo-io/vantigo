import { useQuery } from "@tanstack/react-query";
import { useParams } from "@tanstack/react-router";
import { customerQueryOptions } from "@vantigo/customers-ui/api/customers";
import { isReadOnlyCustomer } from "@vantigo/customers-ui/lib/customer-read-only";
import { useI18n } from "@vantigo/frontend-shell";
import { CustomerInvoicesPanel } from "@vantigo/invoices-ui";
import { fetchSession, sessionQueryKey } from "../../api/auth";
import { getAuthorizationMe } from "../../api/authorization";
import { ModuleNotEnabledPage } from "../../components/errors";
import { enabledModuleKeys } from "../../lib/enabled-modules";
import { hasPermissions } from "../../navigation";
import { useInvoiceAccess } from "../invoices/-invoice-access";
import "../../i18n";

/**
 * The customer page's Invoices tab (payments and delivery design D8). It lives
 * in the customers app but calls the invoices API, so it gates on the invoices
 * module itself: a pasted link must not hit an API 404. The panel is the
 * package's; whether this caller may make a draft from it is the host's
 * answer: with `invoices:create` and `customers:view` (a draft names its buyer
 * from the customers module), and only on a customer the server would take a
 * draft for — active, neither merged away nor anonymised. An archived or
 * disabled customer, or one still loading, gets no "New invoice".
 *
 * It sits beside the route file rather than inside it because the route file
 * may export nothing but its `Route` without costing the bundle a code split.
 */
export const CustomerInvoicesTab = () => {
  const { t } = useI18n("host");
  const { customerId } = useParams({ from: "/customers/$customerId" });
  // The same keys the root layout and the access hook use, so these read its
  // cache: the hook answers customers:view and "Vår ref.", this invoices:create.
  const access = useInvoiceAccess();
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
  if (!enabledModuleKeys().includes("invoices")) return <ModuleNotEnabledPage appLabel={t("navigation.invoices")} />;
  return (
    <CustomerInvoicesPanel
      customerId={customerId}
      canCreate={
        hasPermissions(authorization.data?.permissions, ["invoices:create"]) &&
        access.canViewCustomers &&
        !isReadOnlyCustomer(customer.data) &&
        customer.data?.status === "active"
      }
      userDisplayName={access.userDisplayName}
    />
  );
};
