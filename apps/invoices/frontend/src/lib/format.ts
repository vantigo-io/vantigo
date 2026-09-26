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
    /** A unit price, which has up to four decimals (numeric(14,4)): 33.3333 stays 33.3333, 100 is 100.00. */
    unitPrice: (amount: number, currency: string) =>
      formatters.formatCurrency(amount, currency, { minimumFractionDigits: 2, maximumFractionDigits: 4 }),
    number: (value: number, maxDecimals = 3) => formatters.formatNumber(value, { maximumFractionDigits: maxDecimals }),
    /** A VAT rate in percent, as the locale writes one: 25 is "25%" in en and "25 %" in nb, 12.5 keeps its decimal. */
    percent: (ratePercent: number) =>
      formatters.formatNumber(ratePercent / 100, { style: "percent", maximumFractionDigits: 2 }),
    date: (date: string) => formatters.formatDate(toUtc(date), { dateStyle: "medium", timeZone: "UTC" }),
  };
};
