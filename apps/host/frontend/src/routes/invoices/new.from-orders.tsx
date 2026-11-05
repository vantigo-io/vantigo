import { createFileRoute } from "@tanstack/react-router";
import { ContentSkeleton } from "@vantigo/frontend-shell";
import { FromOrdersPage } from "@vantigo/invoices-ui/pages/from-orders";
import { useInvoiceAccess } from "../../lib/invoice-access";

/** A positive whole number from the URL, else undefined. */
const idOf = (value: unknown): number | undefined => {
  const n = Number(value);
  return Number.isInteger(n) && n > 0 ? n : undefined;
};

/**
 * New invoice → From orders (orders design O39), the package's
 * `FROM_ORDERS_ROUTE_PATH`: `?orderId=` starts with that order chosen — an
 * order's *Invoice* button — and `?customerId=` with that customer. The guard
 * is the Invoices prefix's (`invoices:access`); the wizard's API asks
 * `invoices:create` too, so a caller without it is told so here. The customer
 * picker needs `customers:view`.
 */
export const Route = createFileRoute("/invoices/new/from-orders")({
  validateSearch: (search: Record<string, unknown>): { orderId?: number; customerId?: number } => ({
    orderId: idOf(search.orderId),
    customerId: idOf(search.customerId),
  }),
  component: FromOrdersRoute,
});

function FromOrdersRoute() {
  const access = useInvoiceAccess();
  const { orderId, customerId } = Route.useSearch();
  if (!access.ready) return <ContentSkeleton rows={4} rowHeight={48} />;
  return (
    <FromOrdersPage
      canCreate={access.canCreateInvoices}
      canViewCustomers={access.canViewCustomers}
      initialOrderId={orderId}
      initialCustomerId={customerId}
    />
  );
}
