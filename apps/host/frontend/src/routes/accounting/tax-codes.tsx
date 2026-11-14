import { createFileRoute } from "@tanstack/react-router";
import { TaxCodesPage } from "@vantigo/accounting-ui/pages/tax-codes";

/** The tax codes (accounting design A5). */
export const Route = createFileRoute("/accounting/tax-codes")({
  component: TaxCodesPage,
});
