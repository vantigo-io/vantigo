import { LineChart } from "@mantine/charts";
import { useI18n } from "@vantigo/frontend-shell";
import type { SpotPriceInterval } from "../api/spot-prices";
import { MARKET_TIME_ZONE, marketUtcOffset } from "../lib/market-time";
import "../i18n";

/**
 * One area's spot prices as a line of NOK/kWh per interval start, read on
 * the market's clock: the hours of a day, or every interval of a month. A
 * price holds for its whole interval, hence the steps.
 */
export const PriceChart = ({ intervals, period }: { intervals: SpotPriceInterval[]; period: "day" | "month" }) => {
  const { t, formatters } = useI18n("energy");
  const labels = intervals.map((item) =>
    formatters.formatDate(item.start, {
      ...(period === "month" ? { day: "numeric", month: "short" } : {}),
      hour: "2-digit",
      minute: "2-digit",
      timeZone: MARKET_TIME_ZONE,
    }),
  );
  // The day the clock goes back has its 02:00 twice: a label that repeats
  // carries the interval's UTC offset, so each run of the hour is its own point.
  const counts = new Map<string, number>();
  for (const label of labels) counts.set(label, (counts.get(label) ?? 0) + 1);
  const data = intervals.map((item, index) => ({
    start: (counts.get(labels[index]) ?? 0) > 1 ? `${labels[index]} ${marketUtcOffset(item.start)}` : labels[index],
    nokPerKwh: item.nokPerKwh,
  }));
  return (
    <LineChart
      h={280}
      data={data}
      dataKey="start"
      series={[{ name: "nokPerKwh", label: t("chartNokPerKwh"), color: "blue.6" }]}
      curveType="stepAfter"
      withDots={period === "day"}
      valueFormatter={(value) => formatters.formatNumber(value, { minimumFractionDigits: 2, maximumFractionDigits: 4 })}
    />
  );
};
