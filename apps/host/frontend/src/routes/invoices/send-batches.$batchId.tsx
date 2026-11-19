import { createFileRoute, notFound } from "@tanstack/react-router";
import { SendBatchPage } from "@vantigo/invoices-ui/pages/send-batch";

/**
 * One Send all and its documents' outcomes, the package's
 * `SEND_BATCH_ROUTE_PATH` (invoices phase 6 design I36). A path that is not a
 * positive whole number never reaches the API: it is a not-found. The guard
 * is the Invoices entry's, `invoices:access`.
 */
export const Route = createFileRoute("/invoices/send-batches/$batchId")({
  params: {
    parse: ({ batchId }) => ({ batchId: /^\d+$/.test(batchId) ? Number(batchId) : Number.NaN }),
    stringify: ({ batchId }) => ({ batchId: String(batchId) }),
  },
  beforeLoad: ({ params }) => {
    if (!Number.isSafeInteger(params.batchId) || params.batchId <= 0) throw notFound();
  },
  component: SendBatchRoute,
});

function SendBatchRoute() {
  const { batchId } = Route.useParams();
  return <SendBatchPage batchId={batchId} />;
}
