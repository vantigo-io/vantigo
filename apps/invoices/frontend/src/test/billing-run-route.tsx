import { useParams } from "@tanstack/react-router";
import { BillingRunPage } from "../pages/billing-run";

/** A billing run's page as the host mounts it: the route owns the id and hands it over as a number. */
export const BillingRunRoute = () => {
  const { runId } = useParams({ strict: false }) as { runId: string };
  return <BillingRunPage runId={Number(runId)} />;
};
