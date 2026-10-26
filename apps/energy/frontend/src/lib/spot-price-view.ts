import { useI18n } from "@vantigo/frontend-shell";
import { isoWeek, MARKET_TIME_ZONE, marketDayKey } from "./market-time";
import "../i18n";

/** The buckets the Prices page charts a summary in. */
export type SummaryBucket = "day" | "week" | "month";

/**
 * The colour of each compared area, in this order and never cycled: the
 * first area chosen is the first colour. Mantine's steps checked for the
 * light and the dark surface alike (lightness band, colour-blind separation
 * between neighbours, contrast against the card).
 */
export const AREA_COLORS = ["blue.8", "orange.8", "teal.8", "grape.7", "pink.7"] as const;

/** A preliminary price is drawn dashed: its own series beside the area's. */
export const PRELIMINARY_DASH = "5 4";
export const preliminaryKey = (area: string) => `${area}_preliminary`;

export const formatPrice =
  (formatNumber: (value: number, options?: Intl.NumberFormatOptions) => string) => (value: number) =>
    formatNumber(value, { minimumFractionDigits: 2, maximumFractionDigits: 4 });

/**
 * A bucket's name on the market's clock: the day, the ISO week, or the
 * month; `long` for the figures above the chart, short for its axis.
 */
export const useBucketLabel = () => {
  const { t, formatters } = useI18n("energy");
  return (start: string, bucket: SummaryBucket, long = false) => {
    if (bucket === "week") {
      const { year, week } = isoWeek(marketDayKey(start));
      return t(long ? "pricesWeekLong" : "pricesWeekShort", { week, year });
    }
    return formatters.formatDate(start, {
      ...(bucket === "day"
        ? long
          ? { dateStyle: "medium" }
          : { day: "numeric", month: "short" }
        : { month: long ? "long" : "short", year: "numeric" }),
      timeZone: MARKET_TIME_ZONE,
    });
  };
};
