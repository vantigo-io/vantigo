import { CompositeChart, type CompositeChartSeries } from "@mantine/charts";
import { ColorSwatch, Group, getThemeColor, Paper, Stack, Text, useMantineTheme } from "@mantine/core";
import { useI18n } from "@vantigo/frontend-shell";
import type { ReactNode } from "react";
import type { SpotPriceSummaryBucket } from "../api/spot-prices";
import {
  AREA_COLORS,
  formatPrice,
  PRELIMINARY_DASH,
  preliminaryKey,
  type SummaryBucket,
  useBucketLabel,
} from "../lib/spot-price-view";
import "../i18n";

export interface AreaBuckets {
  area: string;
  buckets: SpotPriceSummaryBucket[];
  /** The area's place among those chosen, which picks its colour; its index here when absent. */
  slot?: number;
}

type Row = Record<string, string | number | null | [number, number]>;

const BAND = "band";

/**
 * The spot prices of one or more areas per day, ISO week or month: the
 * time-weighted average as a line per area, and — for a single area — the
 * lowest and highest price of each bucket as a band behind it. A bucket with
 * a preliminary price is drawn dashed.
 *
 * A `CompositeChart` rather than an `AreaChart`: its `area` series hands the
 * row's value straight to recharts' `Area`, which draws a `[min, max]` pair
 * as a range band, and lines sit on the same axis beside it. `AreaChart`
 * builds its areas for stacking and gradients and has no line series.
 */
export const PriceSummaryChart = ({ areas, bucket }: { areas: AreaBuckets[]; bucket: SummaryBucket }) => {
  const { t, formatters } = useI18n("energy");
  const theme = useMantineTheme();
  const bucketLabel = useBucketLabel();
  const price = formatPrice(formatters.formatNumber);
  const withBand = areas.length === 1;

  const starts = [...new Set(areas.flatMap((item) => item.buckets.map((entry) => entry.start)))].sort(
    (a, b) => Date.parse(a) - Date.parse(b),
  );
  const rows: Row[] = starts.map((start) => ({ start: bucketLabel(start, bucket) }));
  const rowOf = new Map(starts.map((start, index) => [start, rows[index]]));
  // Each row's bucket per area, for what the tooltip says of its coverage.
  const bucketsOf = new Map<Row, Map<string, SpotPriceSummaryBucket>>(rows.map((row) => [row, new Map()]));

  const lines = areas.flatMap(({ area, buckets, slot }, index): CompositeChartSeries[] => {
    const color = AREA_COLORS[(slot ?? index) % AREA_COLORS.length];
    const hasPreliminary = buckets.some((entry) => entry.preliminary);
    for (const row of rows) {
      row[area] = null;
      if (hasPreliminary) row[preliminaryKey(area)] = null;
    }
    buckets.forEach((entry, position) => {
      const row = rowOf.get(entry.start);
      if (!row) return;
      bucketsOf.get(row)?.set(area, entry);
      if (withBand) row[BAND] = [entry.minNokPerKwh, entry.maxNokPerKwh];
      // The dashed line starts at the last final bucket, so the two lines join.
      const nextPreliminary = buckets[position + 1]?.preliminary ?? false;
      if (!entry.preliminary) row[area] = entry.avgNokPerKwh;
      if (entry.preliminary || nextPreliminary) row[preliminaryKey(area)] = entry.avgNokPerKwh;
    });
    return [
      { name: area, label: area, color, type: "line" },
      ...(hasPreliminary
        ? [
            {
              name: preliminaryKey(area),
              label: t("pricesPreliminarySeries", { area }),
              color,
              type: "line" as const,
              strokeDasharray: PRELIMINARY_DASH,
            },
          ]
        : []),
    ];
  });
  const series: CompositeChartSeries[] = withBand
    ? [{ name: BAND, label: t("pricesBand"), color: lines[0]?.color ?? AREA_COLORS[0], type: "area" }, ...lines]
    : lines;

  // With no dots, a point whose neighbours are both gaps (a lone day between
  // missing ones, a lone preliminary bucket) draws nothing: it gets a dot.
  const lone = (key: string, index: number) =>
    typeof rows[index]?.[key] === "number" &&
    typeof rows[index - 1]?.[key] !== "number" &&
    typeof rows[index + 1]?.[key] !== "number";
  const lineProps = (item: CompositeChartSeries) => ({
    dot: ({ cx, cy, index }: { cx?: number; cy?: number; index?: number }) => {
      const key = `${item.name}-dot-${index}`;
      if (index === undefined || cx === undefined || cy === undefined || !lone(item.name, index))
        return <g key={key} />;
      const color = getThemeColor(item.color, theme);
      return <circle key={key} cx={cx} cy={cy} r={3} fill={color} stroke={color} />;
    },
  });

  // The tooltip is the page's own: Mantine's would show a band as its width.
  const tooltip = ({ label, payload }: { label?: ReactNode; payload?: ReadonlyArray<{ payload?: unknown }> }) => {
    const row = payload?.[0]?.payload as Row | undefined;
    if (!row) return null;
    const band = row[BAND];
    return (
      <Paper withBorder shadow="md" radius="md" p="xs">
        <Stack gap={4}>
          <Text size="sm" fw={500}>
            {label}
          </Text>
          {areas.map(({ area, slot }, index) => {
            const value = row[area] ?? row[preliminaryKey(area)];
            if (typeof value !== "number") return null;
            const color = AREA_COLORS[(slot ?? index) % AREA_COLORS.length];
            const [name, shade] = color.split(".");
            const entry = bucketsOf.get(row)?.get(area);
            const minutes = entry ? (Date.parse(entry.end) - Date.parse(entry.start)) / 60_000 : 0;
            const hours = (count: number) => formatters.formatNumber(count / 60, { maximumFractionDigits: 1 });
            return (
              <Stack key={area} gap={0}>
                <Group gap="xs" wrap="nowrap">
                  <ColorSwatch color={theme.colors[name][Number(shade)]} size={10} withShadow={false} />
                  <Text size="sm">
                    {area}: {price(value)} {t("chartNokPerKwh")}
                  </Text>
                </Group>
                {entry && entry.coveredMinutes < minutes && (
                  <Text size="xs" c="dimmed">
                    {t("pricesIncomplete", { hours: hours(entry.coveredMinutes), total: hours(minutes) })}
                  </Text>
                )}
              </Stack>
            );
          })}
          {Array.isArray(band) && (
            <Text size="xs" c="dimmed">
              {t("pricesBand")}: {price(band[0])}–{price(band[1])}
            </Text>
          )}
        </Stack>
      </Paper>
    );
  };

  return (
    <CompositeChart
      h={280}
      data={rows}
      dataKey="start"
      series={series}
      curveType="linear"
      connectNulls={false}
      withDots={false}
      withLegend={areas.length > 1}
      yAxisLabel={t("chartNokPerKwh")}
      areaProps={{ strokeWidth: 0 }}
      lineProps={lineProps}
      valueFormatter={price}
      tooltipProps={{ content: tooltip }}
    />
  );
};
