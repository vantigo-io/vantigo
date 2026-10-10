import { createFileRoute, notFound } from "@tanstack/react-router";
import { ContentSkeleton } from "@vantigo/frontend-shell";
import { InvoicePage } from "@vantigo/invoices-ui/pages/invoice";
import { useInvoiceAccess } from "../../lib/invoice-access";

/**
 * One invoice or credit note. The path is the package's own
 * `INVOICE_ROUTE_PATH` — the list and the create and credit flows navigate to
 * it. A path that is not a positive whole number never reaches the API: it is
 * a not-found, which the `/invoices` layout's own boundary renders.
 */
export const Route = createFileRoute("/invoices/$invoiceId")({
  params: {
    parse: ({ invoiceId }) => ({ invoiceId: /^\d+$/.test(invoiceId) ? Number(invoiceId) : Number.NaN }),
    stringify: ({ invoiceId }) => ({ invoiceId: String(invoiceId) }),
  },
  beforeLoad: ({ params }) => {
    if (!Number.isSafeInteger(params.invoiceId) || params.invoiceId <= 0) throw notFound();
  },
  component: InvoiceRoute,
});

function InvoiceRoute() {
  const { invoiceId } = Route.useParams();
  const access = useInvoiceAccess();
  if (!access.ready) return <ContentSkeleton rows={4} rowHeight={48} />;
  return <InvoicePage invoiceId={invoiceId} canViewCustomers={access.canViewCustomers} currentUserId={access.userId} />;
}
