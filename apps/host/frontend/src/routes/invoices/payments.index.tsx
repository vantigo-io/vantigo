import { createFileRoute } from "@tanstack/react-router";
import { ContentSkeleton } from "@vantigo/frontend-shell";
import { PaymentsPage } from "@vantigo/invoices-ui/pages/payments";
import { useInvoiceAccess } from "../../lib/invoice-access";

/**
 * The Payments area (invoices payments and reminders design D22): the bank
 * file upload, the accounts, the files and the exception queue. The guard is
 * the nav registry's — its Payments entry asks for `invoices:payments`.
 */
export const Route = createFileRoute("/invoices/payments/")({
  component: PaymentsRoute,
});

function PaymentsRoute() {
  const access = useInvoiceAccess();
  if (!access.ready) return <ContentSkeleton rows={4} rowHeight={48} />;
  return <PaymentsPage currentUserId={access.userId} />;
}
