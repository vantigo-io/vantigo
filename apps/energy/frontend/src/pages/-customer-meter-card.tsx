import { AreaChart } from "@mantine/charts";
import { Badge, Card, Group, Stack, Table, Text, Title } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import { type CustomerMeteringPoint, customerConsumptionQueryOptions, type SupplyPeriod } from "../api/energy";

const formatDate = (value: string | null | undefined) => (value ? new Date(value).toLocaleDateString() : "Open-ended");
const startOfPeriod = (periods: SupplyPeriod[]) =>
  periods.filter((period) => period.status === "Active").sort((a, b) => a.start.localeCompare(b.start))[0]?.start ??
  periods[0]?.start;
const endOfPeriod = (periods: SupplyPeriod[]) =>
  periods
    .filter((period) => period.status === "Active")
    .sort((a, b) => (a.end ?? "9999").localeCompare(b.end ?? "9999"))[0]?.end ?? new Date().toISOString();

export const CustomerMeterCard = ({ item, customerId }: { item: CustomerMeteringPoint; customerId: number }) => {
  const from =
    startOfPeriod(item.supplyPeriods) ?? new Date(new Date().setMonth(new Date().getMonth() - 1)).toISOString();
  const to = endOfPeriod(item.supplyPeriods);
  const { data: consumption } = useQuery(customerConsumptionQueryOptions(customerId, item.meteringPoint.id, from, to));
  const chartData = (consumption ?? []).map((interval) => ({
    date: formatDate(interval.start),
    quantityKwh: interval.quantityKwh,
  }));
  return (
    <Card withBorder>
      <Stack>
        <Group justify="space-between">
          <Title order={3}>{item.meteringPoint.gsrn}</Title>
          <Badge>{item.meteringPoint.connectionStatus}</Badge>
        </Group>
        <Text>
          {item.meteringPoint.meterNumber ?? "—"} · {item.meteringPoint.address.streetAddress},{" "}
          {item.meteringPoint.address.city}
        </Text>
        <Text size="sm" c="dimmed">
          Supply period: {formatDate(from)} – {formatDate(to)}
        </Text>
        {chartData.length > 0 ? (
          <AreaChart
            h={220}
            data={chartData}
            dataKey="date"
            series={[{ name: "quantityKwh", label: "kWh", color: "blue.6" }]}
            curveType="natural"
            withDots
          />
        ) : (
          <Text c="dimmed">No consumption readings in this supply period.</Text>
        )}
        {consumption && consumption.length > 0 && (
          <Table>
            <Table.Thead>
              <Table.Tr>
                <Table.Th>Start</Table.Th>
                <Table.Th>End</Table.Th>
                <Table.Th>Quantity (kWh)</Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {consumption.map((interval) => (
                <Table.Tr key={interval.id}>
                  <Table.Td>{formatDate(interval.start)}</Table.Td>
                  <Table.Td>{formatDate(interval.end)}</Table.Td>
                  <Table.Td>{interval.quantityKwh}</Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        )}
      </Stack>
    </Card>
  );
};
