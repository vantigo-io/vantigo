import { CompositeChart, type CompositeChartSeries } from "@mantine/charts";
import { ColorSwatch, Group, Paper, Stack, Text, useMantineTheme } from "@mantine/core";
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

  const lines = areas.flatMap(({ area, buckets }, index): CompositeChartSeries[] => {
    const color = AREA_COLORS[index % AREA_COLORS.length];
    const hasPreliminary = buckets.some((entry) => entry.preliminary);
    for (const row of rows) {
      row[area] = null;
      if (hasPreliminary) row[preliminaryKey(area)] = null;
    }
    buckets.forEach((entry, position) => {
      const row = rowOf.get(entry.start);
      if (!row) return;
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
          {areas.map(({ area }, index) => {
            const value = row[area] ?? row[preliminaryKey(area)];
            if (typeof value !== "number") return null;
            const color = AREA_COLORS[index % AREA_COLORS.length];
            const [name, shade] = color.split(".");
            return (
              <Group key={area} gap="xs" wrap="nowrap">
                <ColorSwatch color={theme.colors[name][Number(shade)]} size={10} withShadow={false} />
                <Text size="sm">
                  {area}: {price(value)} {t("chartNokPerKwh")}
                </Text>
              </Group>
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
      valueFormatter={price}
      tooltipProps={{ content: tooltip }}
    />
  );
};
