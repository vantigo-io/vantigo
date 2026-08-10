import { AreaChart } from "@mantine/charts";
import { Text } from "@mantine/core";
import dayjs from "dayjs";
import type { ConsumptionAggregate, ConsumptionResolution } from "../api/energy";

export const ConsumptionChart = ({
  aggregates,
  resolution,
  color = "teal.6",
}: {
  aggregates: ConsumptionAggregate[];
  resolution: ConsumptionResolution;
  color?: string;
}) => {
  if (aggregates.length === 0) return <Text c="dimmed">No consumption readings for this period.</Text>;
  const data = aggregates.map((item) => ({
    date: dayjs(item.bucketStart).format(
      resolution === "hour" ? "MMM D HH:mm" : resolution === "day" ? "MMM D" : "MMM YYYY",
    ),
    quantityKwh: item.quantityKwh,
  }));
  return (
    <AreaChart
      h={240}
      data={data}
      dataKey="date"
      series={[{ name: "quantityKwh", label: "kWh", color }]}
      curveType="natural"
      withDots
    />
  );
};
