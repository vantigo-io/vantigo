import { LineChart } from "@mantine/charts";
import { useI18n } from "@vantigo/frontend-shell";
import type { SpotPriceInterval } from "../api/spot-prices";
import { MARKET_TIME_ZONE, marketUtcOffset } from "../lib/market-time";
import { AREA_COLORS, formatPrice, PRELIMINARY_DASH, preliminaryKey } from "../lib/spot-price-view";
import "../i18n";

export interface AreaIntervals {
  area: string;
  intervals: SpotPriceInterval[];
}

/**
 * The spot prices of one or more areas as lines of NOK/kWh per interval
 * start, read on the market's clock: the intervals of a day, with a dot on
 * each, or of a week. A price holds for its whole interval, hence the
 * steps. A preliminary interval is drawn dashed.
 */
export const PriceChart = ({ areas, period }: { areas: AreaIntervals[]; period: "day" | "week" }) => {
  const { t, formatters } = useI18n("energy");
  const starts = [...new Set(areas.flatMap((item) => item.intervals.map((interval) => interval.start)))].sort(
    (a, b) => Date.parse(a) - Date.parse(b),
  );
  const labels = starts.map((start) =>
    formatters.formatDate(start, {
      ...(period === "week" ? { weekday: "short" } : {}),
      hour: "2-digit",
      minute: "2-digit",
      timeZone: MARKET_TIME_ZONE,
    }),
  );
  // The day the clock goes back has its 02:00 twice: a label that repeats
  // carries the interval's UTC offset, so each run of the hour is its own point.
  const counts = new Map<string, number>();
  for (const label of labels) counts.set(label, (counts.get(label) ?? 0) + 1);
  const rows: Array<Record<string, string | number | null>> = starts.map((start, index) => ({
    start: (counts.get(labels[index]) ?? 0) > 1 ? `${labels[index]} ${marketUtcOffset(start)}` : labels[index],
  }));
  const rowOf = new Map(starts.map((start, index) => [start, rows[index]]));

  const series = areas.flatMap(({ area, intervals }, index) => {
    const color = AREA_COLORS[index % AREA_COLORS.length];
    const hasPreliminary = intervals.some((item) => item.state === "preliminary");
    for (const row of rows) {
      row[area] = null;
      if (hasPreliminary) row[preliminaryKey(area)] = null;
    }
    intervals.forEach((item, position) => {
      const row = rowOf.get(item.start);
      if (!row) return;
      const previousFinal = position > 0 && intervals[position - 1].state !== "preliminary";
      // The solid line runs up to the first preliminary interval, so the last
      // final price's step reaches it; from there the dashed line takes over.
      if (item.state !== "preliminary" || previousFinal) row[area] = item.nokPerKwh;
      if (item.state === "preliminary") row[preliminaryKey(area)] = item.nokPerKwh;
    });
    return [
      { name: area, label: area, color },
      ...(hasPreliminary
        ? [
            {
              name: preliminaryKey(area),
              label: t("pricesPreliminarySeries", { area }),
              color,
              strokeDasharray: PRELIMINARY_DASH,
            },
          ]
        : []),
    ];
  });

  return (
    <LineChart
      h={280}
      data={rows}
      dataKey="start"
      series={series}
      curveType="stepAfter"
      connectNulls={false}
      withDots={period === "day"}
      withLegend={areas.length > 1}
      yAxisLabel={t("chartNokPerKwh")}
      valueFormatter={formatPrice(formatters.formatNumber)}
    />
  );
};
