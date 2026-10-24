import { LineChart } from "@mantine/charts";
import { useI18n } from "@vantigo/frontend-shell";
import type { SpotPriceInterval } from "../api/spot-prices";
import { MARKET_TIME_ZONE } from "../lib/market-time";
import "../i18n";

/**
 * One area's spot prices as a line of NOK/kWh per interval start, read on
 * the market's clock: the hours of a day, or every interval of a month. A
 * price holds for its whole interval, hence the steps.
 */
export const PriceChart = ({ intervals, period }: { intervals: SpotPriceInterval[]; period: "day" | "month" }) => {
  const { t, formatters } = useI18n("energy");
  const data = intervals.map((item) => ({
    start: formatters.formatDate(item.start, {
      ...(period === "month" ? { day: "numeric", month: "short" } : {}),
      hour: "2-digit",
      minute: "2-digit",
      timeZone: MARKET_TIME_ZONE,
    }),
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
