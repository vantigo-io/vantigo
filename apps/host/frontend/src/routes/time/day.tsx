import { createFileRoute } from "@tanstack/react-router";
import { isIsoDate } from "@vantigo/time-ui/lib/week";
import { DayPage, type DaySearch } from "@vantigo/time-ui/pages/day";
import { useInvoiceHref } from "../../lib/invoice-access";

/**
 * The day view, with where an invoice lives when the caller may open one: an
 * entry the Invoices module invoiced links its "Invoiced by invoice n" there.
 */
const TimeDay = () => <DayPage invoiceHref={useInvoiceHref()} />;

export const Route = createFileRoute("/time/day")({
  // `?date=` is the package's own deep link — `dayUrl` builds it and My week's
  // day headings point at it. Without a date the page stands on today.
  validateSearch: (search: Record<string, unknown>): DaySearch => ({
    date: isIsoDate(search.date) ? search.date : undefined,
  }),
  component: TimeDay,
});
