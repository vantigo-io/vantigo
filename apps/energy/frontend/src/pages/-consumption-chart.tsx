import { AreaChart } from "@mantine/charts";
import { Text } from "@mantine/core";
import { useI18n } from "@vantigo/frontend-shell";
import type { ConsumptionAggregate, ConsumptionResolution } from "../api/energy";
import "../i18n";

export const ConsumptionChart = ({
  aggregates,
  resolution,
  color = "teal.6",
}: {
  aggregates: ConsumptionAggregate[];
  resolution: ConsumptionResolution;
  color?: string;
}) => {
  const { t, formatters } = useI18n("energy");
  if (aggregates.length === 0) return <Text c="dimmed">{t("noConsumptionReadings")}</Text>;
  const data = aggregates.map((item) => ({
    date: formatters.formatDate(item.bucketStart, {
      month: "short",
      ...(resolution !== "month" ? { day: "numeric" } : {}),
      ...(resolution === "hour" ? { hour: "2-digit", minute: "2-digit" } : {}),
    }),
    quantityKwh: item.quantityKwh,
  }));
  return (
    <AreaChart
      h={240}
      data={data}
      dataKey="date"
      series={[{ name: "quantityKwh", label: t("chartQuantity"), color }]}
      curveType="natural"
      withDots
    />
  );
};
