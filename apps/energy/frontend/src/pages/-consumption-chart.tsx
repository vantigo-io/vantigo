import { AreaChart } from "@mantine/charts";
import { Text } from "@mantine/core";
import type { ConsumptionInterval } from "../api/energy";

export const ConsumptionChart = ({ intervals }: { intervals: ConsumptionInterval[] }) => {
  if (intervals.length === 0) return <Text c="dimmed">No consumption readings for this period.</Text>;
  const data = intervals.map((item) => ({
    date: new Date(item.start).toLocaleDateString(),
    quantityKwh: item.quantityKwh,
  }));
  return (
    <AreaChart
      h={240}
      data={data}
      dataKey="date"
      series={[{ name: "quantityKwh", label: "kWh", color: "teal.6" }]}
      curveType="natural"
      withDots
    />
  );
};
