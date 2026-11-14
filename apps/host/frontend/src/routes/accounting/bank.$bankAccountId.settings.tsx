import { createFileRoute, notFound } from "@tanstack/react-router";
import { BankAccountSettingsPage } from "@vantigo/accounting-ui/pages/bank";
import { isPositiveId } from "./-ids";

/** A bank account's record, statement source and CSV mapping (accounting design A26, A27, A29). */
export const Route = createFileRoute("/accounting/bank/$bankAccountId/settings")({
  beforeLoad: ({ params }) => {
    if (!isPositiveId(params.bankAccountId)) throw notFound();
  },
  component: BankAccountSettingsPage,
});
