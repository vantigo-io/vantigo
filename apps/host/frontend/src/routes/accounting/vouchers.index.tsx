import { createFileRoute } from "@tanstack/react-router";
import { VouchersPage } from "@vantigo/accounting-ui/pages/vouchers";

/** Every voucher, newest first (accounting design A4). */
export const Route = createFileRoute("/accounting/vouchers/")({
  component: VouchersPage,
});
