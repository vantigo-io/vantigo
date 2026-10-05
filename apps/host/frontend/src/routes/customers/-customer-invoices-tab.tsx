import { useQuery } from "@tanstack/react-query";
import { useParams } from "@tanstack/react-router";
import { customerQueryOptions } from "@vantigo/customers-ui/api/customers";
import { isReadOnlyCustomer } from "@vantigo/customers-ui/lib/customer-read-only";
import { useI18n } from "@vantigo/frontend-shell";
import { CustomerInvoicesPanel, UninvoicedWorkPanel } from "@vantigo/invoices-ui";
import { ModuleNotEnabledPage } from "../../components/errors";
import { enabledModuleKeys } from "../../lib/enabled-modules";
import { useInvoiceAccess } from "../../lib/invoice-access";
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
 * Above the list sits the customer's work not yet invoiced (invoices work
 * design D18), for a caller who may draft an invoice of it — `invoices:create`
 * alone, since the view lists the hours, the people and the rates the invoice
 * will state and the wizard names the customer by id. The panel says nothing
 * on an installation that invoices no work.
 *
 * It sits beside the route file rather than inside it because the route file
 * may export nothing but its `Route` without costing the bundle a code split.
 */
export const CustomerInvoicesTab = () => {
  const { t } = useI18n("host");
  const { customerId } = useParams({ from: "/customers/$customerId" });
  // The root layout's session and authorization, read from its cache.
  const access = useInvoiceAccess();
  // The layout's loader has already put the customer in the cache.
  const customer = useQuery(customerQueryOptions(customerId));
  if (!enabledModuleKeys().includes("invoices")) return <ModuleNotEnabledPage appLabel={t("navigation.invoices")} />;
  return (
    <>
      {access.canCreateInvoices && <UninvoicedWorkPanel customerId={customerId} />}
      <CustomerInvoicesPanel
        customerId={customerId}
        canCreate={
          access.canCreateInvoices &&
          access.canViewCustomers &&
          !isReadOnlyCustomer(customer.data) &&
          customer.data?.status === "active"
        }
        userDisplayName={access.userDisplayName}
      />
    </>
  );
};
