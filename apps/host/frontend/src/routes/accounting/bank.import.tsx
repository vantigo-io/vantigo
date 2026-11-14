import { createFileRoute } from "@tanstack/react-router";
import { BankImportPage } from "@vantigo/accounting-ui/pages/bank";

/** Import a camt.053 or CSV statement (accounting design A27). */
export const Route = createFileRoute("/accounting/bank/import")({
  component: BankImportPage,
});
