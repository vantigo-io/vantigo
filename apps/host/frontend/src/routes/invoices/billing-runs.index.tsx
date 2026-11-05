import { createFileRoute } from "@tanstack/react-router";
import { BillingRunsPage } from "@vantigo/invoices-ui/pages/billing-runs";
import { enabledModuleKeys } from "../../lib/enabled-modules";

/**
 * Billing runs (energy consumption billing design D10, D11), the package's
 * `BILLING_RUNS_ROUTE_PATH`. The guard is the nav registry's — its Billing
 * runs entry asks for `invoices:access` and `invoices:create`; issuing is the
 * page's own, behind meta's `canIssue`. A run may invoice orders while the
 * Orders module is on (orders design O40).
 */
export const Route = createFileRoute("/invoices/billing-runs/")({
  component: BillingRunsRoute,
});

function BillingRunsRoute() {
  return <BillingRunsPage ordersEnabled={enabledModuleKeys().includes("orders")} />;
}
