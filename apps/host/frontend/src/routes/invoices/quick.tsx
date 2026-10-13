import { createFileRoute } from "@tanstack/react-router";
import { ContentSkeleton } from "@vantigo/frontend-shell";
import { QuickInvoicePage } from "@vantigo/invoices-ui/pages/quick-invoice";
import { useInvoiceAccess } from "../../lib/invoice-access";

/**
 * The quick invoice (invoices payments and reminders design D17, D22): a
 * customer and a few lines, issued at once and, where allowed, paid on the
 * spot with Vipps. The guard is the nav registry's — its entry asks for
 * `invoices:access` and both `invoices:create` and `invoices:issue`; the page
 * itself gates on meta's `canQuickInvoice` too. The picker needs
 * `customers:view`, and "New customer" `customers:create` and `customers:update`.
 */
export const Route = createFileRoute("/invoices/quick")({
  component: QuickInvoiceRoute,
});

function QuickInvoiceRoute() {
  const access = useInvoiceAccess();
  if (!access.ready) return <ContentSkeleton rows={4} rowHeight={48} />;
  return <QuickInvoicePage canViewCustomers={access.canViewCustomers} canCreateCustomers={access.canCreateCustomers} />;
}
