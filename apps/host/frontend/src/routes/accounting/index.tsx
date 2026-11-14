import { createFileRoute } from "@tanstack/react-router";
import { AccountingOverviewPage } from "@vantigo/accounting-ui/pages/overview";

/** The app's home: the setup state, the lock date, the year's vouchers and the shortcuts (accounting design R12). */
export const Route = createFileRoute("/accounting/")({
  component: AccountingOverviewPage,
});
