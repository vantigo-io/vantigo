import { createFileRoute } from "@tanstack/react-router";
import { BankRulesPage } from "@vantigo/accounting-ui/pages/bank";

/** The bank rules a line is coded by (accounting design A28). */
export const Route = createFileRoute("/accounting/bank/rules")({
  component: BankRulesPage,
});
