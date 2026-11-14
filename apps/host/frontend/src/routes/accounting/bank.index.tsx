import { createFileRoute } from "@tanstack/react-router";
import { BankAccountsPage } from "@vantigo/accounting-ui/pages/bank";

/** The bank accounts and their lines not matched (accounting design A26). */
export const Route = createFileRoute("/accounting/bank/")({
  component: BankAccountsPage,
});
