import { createFileRoute } from "@tanstack/react-router";
import { ContentSkeleton } from "@vantigo/frontend-shell";
import { OverduePage } from "@vantigo/invoices-ui/pages/overdue";
import { useInvoiceAccess } from "../../lib/invoice-access";

/**
 * The Overdue area (invoices payments and reminders design D22): the overdue
 * list, the run's preview and the runs. The guard is the nav registry's — its
 * Overdue entry asks for `invoices:access`; sending reminders is the page's
 * own, behind meta's `canRunReminders`.
 */
export const Route = createFileRoute("/invoices/overdue")({
  component: OverdueRoute,
});

function OverdueRoute() {
  const access = useInvoiceAccess();
  if (!access.ready) return <ContentSkeleton rows={4} rowHeight={48} />;
  return <OverduePage canViewCustomers={access.canViewCustomers} currentUserId={access.userId} />;
}
