import { createFileRoute } from "@tanstack/react-router";
import { AccountsPage } from "@vantigo/accounting-ui/pages/accounts";

/** The chart of accounts (accounting design A3). */
export const Route = createFileRoute("/accounting/accounts")({
  component: AccountsPage,
});
