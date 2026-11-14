import { createFileRoute, notFound } from "@tanstack/react-router";
import { ReconciliationPage } from "@vantigo/accounting-ui/pages/reconciliation";
import { isPositiveId } from "./-ids";

/** A bank account's reconciliation (accounting design A28). A path that is not a positive whole number is the layout's not-found. */
export const Route = createFileRoute("/accounting/bank/$bankAccountId/")({
  beforeLoad: ({ params }) => {
    if (!isPositiveId(params.bankAccountId)) throw notFound();
  },
  component: ReconciliationPage,
});
