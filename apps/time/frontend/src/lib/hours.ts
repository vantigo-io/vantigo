import { useI18n } from "@vantigo/frontend-shell";
import "../i18n";
import { formatHours } from "./week";

/**
 * Hours in the caller's language: `display` for a total or a list (grouped,
 * at most two decimals), `input` for a field the caller may type back into,
 * written with the decimal separator they would type themselves.
 */
export const useHoursFormat = () => {
  const { formatters } = useI18n("time");
  const separator = formatters.formatNumber(1.5).includes(",") ? "," : ".";
  return {
    display: (hours: number) => formatters.formatNumber(hours, { maximumFractionDigits: 2 }),
    input: (hours: number) => formatHours(hours, separator),
  };
};
