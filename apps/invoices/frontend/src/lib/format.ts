import { useI18n } from "@vantigo/frontend-shell";
import "../i18n";

/** A calendar date read and written in UTC, so a day never slides by one. */
const toUtc = (date: string): Date => new Date(`${date}T00:00:00.000Z`);

/**
 * The one way this package writes money, numbers and dates. Money is written
 * in the document's own currency; a calendar date in UTC.
 */
export const useInvoiceFormat = () => {
  const { t, formatters } = useI18n("invoices");
  return {
    t,
    money: (amount: number, currency: string) => formatters.formatCurrency(amount, currency),
    number: (value: number, maxDecimals = 3) => formatters.formatNumber(value, { maximumFractionDigits: maxDecimals }),
    date: (date: string) => formatters.formatDate(toUtc(date), { dateStyle: "medium", timeZone: "UTC" }),
  };
};
