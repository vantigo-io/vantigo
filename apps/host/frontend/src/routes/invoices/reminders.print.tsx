import { createFileRoute } from "@tanstack/react-router";
import { ContentSkeleton } from "@vantigo/frontend-shell";
import { ReminderPrintPage } from "@vantigo/invoices-ui/pages/reminder-print";
import { useInvoiceAccess } from "../../lib/invoice-access";

/**
 * Paper letters, the package's `REMINDER_PRINT_ROUTE_PATH`: the letters
 * awaiting print, printed in batches for a posting day and confirmed posted.
 * The guard is the list's `/invoices` entry, `invoices:access`; the page
 * itself says "not allowed" without `invoices:payments` (meta's
 * `canRunReminders`).
 */
export const Route = createFileRoute("/invoices/reminders/print")({
  component: ReminderPrintRoute,
});

function ReminderPrintRoute() {
  const access = useInvoiceAccess();
  if (!access.ready) return <ContentSkeleton rows={4} rowHeight={48} />;
  return <ReminderPrintPage currentUserId={access.userId} />;
}
