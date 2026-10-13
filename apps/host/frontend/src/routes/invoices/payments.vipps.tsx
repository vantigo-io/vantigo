import { createFileRoute } from "@tanstack/react-router";
import { VippsPaymentsPage } from "@vantigo/invoices-ui/pages/vipps-payments";

/**
 * The Payments area's Vipps tab (invoices payments and reminders design D17,
 * D22): one day's Vipps payments, the CSV and the day's reconciliation, the
 * package's `VIPPS_PAYMENTS_ROUTE_PATH`. Its read is `invoices:access`, but
 * the URL is under the Payments area, whose guard — the nav registry's
 * Payments entry — asks for `invoices:payments`; reconciling is offered by
 * meta's `canRegisterPayments`.
 */
export const Route = createFileRoute("/invoices/payments/vipps")({
  component: VippsPaymentsPage,
});
