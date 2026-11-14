import { createFileRoute, notFound } from "@tanstack/react-router";
import { VoucherPage } from "@vantigo/accounting-ui/pages/voucher";
import { isPositiveId } from "./-ids";

/** One voucher (accounting design A4, A7). A path that is not a positive whole number is the layout's not-found. */
export const Route = createFileRoute("/accounting/vouchers/$voucherId")({
  beforeLoad: ({ params }) => {
    if (!isPositiveId(params.voucherId)) throw notFound();
  },
  component: VoucherPage,
});
