import { ContentSkeleton } from "@vantigo/frontend-shell";
import { InvoicesPage } from "@vantigo/invoices-ui/pages/invoices";
import "../../i18n";
import { enabledModuleKeys } from "../../lib/enabled-modules";
import { useInvoiceAccess } from "../../lib/invoice-access";

/**
 * The list's entry point: the package page, told whether the caller may pick
 * a buyer, whose name "Vår ref." starts as, and whether Orders is on, so
 * "From orders" is offered. It sits beside the route file
 * because a route file may export nothing but its `Route`.
 */
export const InvoicesList = () => {
  const access = useInvoiceAccess();
  if (!access.ready) return <ContentSkeleton rows={4} rowHeight={48} />;
  return (
    <InvoicesPage
      canViewCustomers={access.canViewCustomers}
      userDisplayName={access.userDisplayName}
      canInvoiceOrders={enabledModuleKeys().includes("orders")}
    />
  );
};
