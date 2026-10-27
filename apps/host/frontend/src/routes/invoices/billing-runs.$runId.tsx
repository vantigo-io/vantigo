import { createFileRoute, notFound } from "@tanstack/react-router";
import { BillingRunPage } from "@vantigo/invoices-ui/pages/billing-run";

/**
 * One billing run and its items, the package's `BILLING_RUN_ROUTE_PATH`. A
 * path that is not a positive whole number never reaches the API: it is a
 * not-found. The guard is the Billing runs entry's, `invoices:access` and
 * `invoices:create`.
 */
export const Route = createFileRoute("/invoices/billing-runs/$runId")({
  params: {
    parse: ({ runId }) => ({ runId: /^\d+$/.test(runId) ? Number(runId) : Number.NaN }),
    stringify: ({ runId }) => ({ runId: String(runId) }),
  },
  beforeLoad: ({ params }) => {
    if (!Number.isSafeInteger(params.runId) || params.runId <= 0) throw notFound();
  },
  component: BillingRunRoute,
});

function BillingRunRoute() {
  const { runId } = Route.useParams();
  return <BillingRunPage runId={runId} />;
}
