import { createFileRoute, notFound } from "@tanstack/react-router";
import { ContentSkeleton } from "@vantigo/frontend-shell";
import { ReminderRunPage } from "@vantigo/invoices-ui/pages/reminder-run";
import { useInvoiceAccess } from "../../lib/invoice-access";

/**
 * One reminder run and its letters, the package's `REMINDER_RUN_ROUTE_PATH`.
 * A path that is not a positive whole number never reaches the API: it is a
 * not-found. The guard is the list's `/invoices` entry, `invoices:access`;
 * the page itself says "not allowed" without `invoices:payments` (meta's
 * `canRunReminders`).
 */
export const Route = createFileRoute("/invoices/reminder-runs/$runId")({
  params: {
    parse: ({ runId }) => ({ runId: /^\d+$/.test(runId) ? Number(runId) : Number.NaN }),
    stringify: ({ runId }) => ({ runId: String(runId) }),
  },
  beforeLoad: ({ params }) => {
    if (!Number.isSafeInteger(params.runId) || params.runId <= 0) throw notFound();
  },
  component: ReminderRunRoute,
});

function ReminderRunRoute() {
  const { runId } = Route.useParams();
  const access = useInvoiceAccess();
  if (!access.ready) return <ContentSkeleton rows={4} rowHeight={48} />;
  return <ReminderRunPage runId={runId} currentUserId={access.userId} />;
}
