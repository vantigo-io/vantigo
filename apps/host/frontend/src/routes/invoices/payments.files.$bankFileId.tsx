import { createFileRoute, notFound } from "@tanstack/react-router";
import { ContentSkeleton } from "@vantigo/frontend-shell";
import { BankFilePage } from "@vantigo/invoices-ui/pages/bank-file";
import { useInvoiceAccess } from "../../lib/invoice-access";

/**
 * One imported bank file's result, the package's `BANK_FILE_ROUTE_PATH`. A
 * path that is not a positive whole number never reaches the API: it is a
 * not-found. The guard is the Payments entry's, `invoices:payments`.
 */
export const Route = createFileRoute("/invoices/payments/files/$bankFileId")({
  params: {
    parse: ({ bankFileId }) => ({ bankFileId: /^\d+$/.test(bankFileId) ? Number(bankFileId) : Number.NaN }),
    stringify: ({ bankFileId }) => ({ bankFileId: String(bankFileId) }),
  },
  beforeLoad: ({ params }) => {
    if (!Number.isSafeInteger(params.bankFileId) || params.bankFileId <= 0) throw notFound();
  },
  component: BankFileRoute,
});

function BankFileRoute() {
  const { bankFileId } = Route.useParams();
  const access = useInvoiceAccess();
  if (!access.ready) return <ContentSkeleton rows={4} rowHeight={48} />;
  return <BankFilePage bankFileId={bankFileId} currentUserId={access.userId} />;
}
